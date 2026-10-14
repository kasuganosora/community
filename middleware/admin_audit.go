package middleware

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
)

// AdminAuditMiddleware must follow authentication/administrator authorization.
// Reserve a durable pending record before executing a mutation: unavailable
// auditing cannot silently allow unrecorded administrative changes.
func AdminAuditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.FullPath(), "/api/v1/admin/") || c.Request.Method == "GET" || c.Request.Method == "HEAD" || c.Request.Method == "OPTIONS" {
			c.Next()
			return
		}
		user, ok := c.Get("user")
		u, valid := user.(*models.User)
		account, exists := c.Get("account")
		a, accountOK := account.(*models.Account)
		if !ok || !valid || u == nil || !c.GetBool("admin_authorized") || !exists || !accountOK || a == nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
			return
		}
		route := c.FullPath()
		if route == "" {
			c.AbortWithStatusJSON(500, gin.H{"error": "Unable to audit this operation"})
			return
		}
		target := ""
		if id, err := strconv.ParseUint(c.Param("id"), 10, 64); err == nil {
			target = strconv.FormatUint(id, 10)
		}
		started := time.Now()
		entry := models.AdminAuditLog{ActorID: u.Id, AccountID: a.Id, Method: c.Request.Method, Route: route, Action: c.Request.Method + " " + route, TargetID: target, Outcome: "pending", CreatedAt: started}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
		err := db.GetDB().WithContext(ctx).Create(&entry).Error
		cancel()
		if err != nil {
			log.Error("Unable to reserve administrative audit record")
			c.AbortWithStatusJSON(503, gin.H{"error": "Audit storage unavailable; operation was not performed"})
			return
		}
		c.Set("admin_audit_id", entry.Id)
		// On panic, keep pending rather than claim successful completion.
		c.Next()
		now := time.Now()
		outcome := "success"
		if c.Writer.Status() >= 400 || len(c.Errors) > 0 {
			outcome = "failure"
		}
		ctx, cancel = context.WithTimeout(context.WithoutCancel(c.Request.Context()), 5*time.Second)
		defer cancel()
		if err := db.GetDB().WithContext(ctx).Model(&entry).Updates(map[string]any{"outcome": outcome, "status": c.Writer.Status(), "duration_ms": now.Sub(started).Milliseconds(), "completed_at": now}).Error; err != nil {
			log.Error("Administrative audit completion failed for record %d; outcome remains pending", entry.Id)
		}
	}
}
