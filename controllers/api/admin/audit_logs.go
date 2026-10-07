package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"strconv"
	"time"
)

func ListAuditLogs(c *gin.Context) {
	q := db.GetDB().WithContext(c.Request.Context()).Model(&models.AdminAuditLog{})
	for _, key := range []string{"actor_id", "max_id"} {
		if value := c.Query(key); value != "" {
			id, err := strconv.ParseUint(value, 10, 64)
			if err != nil || id == 0 {
				c.JSON(422, gin.H{"error": "Invalid " + key})
				return
			}
			if key == "actor_id" {
				q = q.Where("actor_id = ?", id)
			} else {
				q = q.Where("id < ?", id)
			}
		}
	}
	if action := c.Query("action"); action != "" {
		q = q.Where("action = ?", action)
	}
	if outcome := c.Query("outcome"); outcome != "" {
		if outcome != "pending" && outcome != "success" && outcome != "failure" {
			c.JSON(422, gin.H{"error": "Invalid outcome"})
			return
		}
		q = q.Where("outcome = ?", outcome)
	}
	for _, key := range []string{"from", "to"} {
		if value := c.Query(key); value != "" {
			t, err := time.Parse(time.RFC3339, value)
			if err != nil {
				c.JSON(422, gin.H{"error": "Dates must be RFC3339"})
				return
			}
			if key == "from" {
				q = q.Where("created_at >= ?", t)
			} else {
				q = q.Where("created_at <= ?", t)
			}
		}
	}
	limit := 40
	if value := c.Query("limit"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n > 80 {
			c.JSON(422, gin.H{"error": "limit must be 1–80"})
			return
		}
		limit = n
	}
	rows := make([]models.AdminAuditLog, 0)
	if err := q.Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		c.JSON(500, gin.H{"error": "Unable to load audit logs"})
		return
	}
	if len(rows) == limit {
		query := c.Request.URL.Query()
		query.Set("max_id", strconv.FormatUint(rows[len(rows)-1].Id, 10))
		c.Header("Link", "<"+c.Request.URL.Path+"?"+query.Encode()+">; rel=\"next\"")
	}
	c.JSON(200, rows)
}
