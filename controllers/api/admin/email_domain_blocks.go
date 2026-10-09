package admin

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func emailDomainBlockJSON(b *models.EmailDomainBlock) gin.H {
	return gin.H{
		"id":         strconv.FormatUint(b.ID, 10),
		"domain":     b.Domain,
		"created_at": b.CreatedAt,
		"history":    []any{},
	}
}

// ListEmailDomainBlocks handles GET /api/v1/admin/email_domain_blocks.
func ListEmailDomainBlocks(c *gin.Context) {
	q := db.GetDB().WithContext(c.Request.Context()).Model(&models.EmailDomainBlock{}).Order("id DESC")
	if v := c.Query("max_id"); v != "" {
		id, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			c.JSON(422, gin.H{"error": "Invalid max_id"})
			return
		}
		q = q.Where("id < ?", id)
	}
	limit := 100
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			c.JSON(422, gin.H{"error": "limit must be 1–200"})
			return
		}
		limit = n
	}
	var rows []*models.EmailDomainBlock
	if err := q.Limit(limit).Find(&rows).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "list email domain blocks"))
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		out = append(out, emailDomainBlockJSON(r))
	}
	c.JSON(200, out)
}

// GetEmailDomainBlock handles GET /api/v1/admin/email_domain_blocks/:id.
func GetEmailDomainBlock(c *gin.Context) {
	b, ok := loadEmailDomainBlock(c)
	if ok {
		c.JSON(200, emailDomainBlockJSON(b))
	}
}

func loadEmailDomainBlock(c *gin.Context) (*models.EmailDomainBlock, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(404, gin.H{"error": "Record not found"})
		return nil, false
	}
	var b models.EmailDomainBlock
	if err := db.GetDB().WithContext(c.Request.Context()).First(&b, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Record not found"})
		} else {
			_ = c.Error(errors.Wrap(err, "load email domain block"))
			c.JSON(500, gin.H{"error": "internal error"})
		}
		return nil, false
	}
	return &b, true
}

// CreateEmailDomainBlock handles POST /api/v1/admin/email_domain_blocks.
func CreateEmailDomainBlock(c *gin.Context) {
	var req struct {
		Domain string `form:"domain" json:"domain"`
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(422, gin.H{"error": err.Error()})
		return
	}
	domain := models.NormalizeEmailDomain(req.Domain)
	if domain == "" || len(domain) > 255 || !validEmailBlockDomain(domain) {
		c.JSON(422, gin.H{"error": "Validation failed: Domain is invalid"})
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var existing models.EmailDomainBlock
	err := conn.Where("domain = ?", domain).First(&existing).Error
	if err == nil {
		c.JSON(422, gin.H{"error": "Validation failed: Domain has already been taken"})
		return
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		_ = c.Error(errors.Wrap(err, "find email domain block"))
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	b := &models.EmailDomainBlock{Domain: domain}
	if err := conn.Create(b).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create email domain block"))
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	c.JSON(200, emailDomainBlockJSON(b))
}

// DeleteEmailDomainBlock handles DELETE /api/v1/admin/email_domain_blocks/:id.
func DeleteEmailDomainBlock(c *gin.Context) {
	b, ok := loadEmailDomainBlock(c)
	if !ok {
		return
	}
	if err := db.GetDB().WithContext(c.Request.Context()).Delete(b).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "delete email domain block"))
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	c.JSON(200, gin.H{})
}

func validEmailBlockDomain(d string) bool {
	if len(d) < 3 || d[0] == '.' || d[len(d)-1] == '.' {
		return false
	}
	dot := false
	for i := 0; i < len(d); i++ {
		ch := d[i]
		switch {
		case ch == '.':
			dot = true
		case ch == '-' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9':
		default:
			return false
		}
	}
	return dot
}
