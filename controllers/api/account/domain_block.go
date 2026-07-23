package account

import (
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"strconv"
)

// DomainBlocksList handles GET /api/v1/domain_blocks.
func DomainBlocksList(c *gin.Context) {
	limit := 80

	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			limit = parsed
		}
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Query domain blocks
	query := db.GetDB().Model(&models.Block{}).
		Where("account_id = ? AND type = ?", account.Id, models.BlockTypeDomain).
		Order("id DESC")

	// Pagination
	if maxId := c.Query("max_id"); maxId != "" {
		if id, err := strconv.ParseUint(maxId, 10, 64); err == nil {
			query = query.Where("id < ?", id)
		}
	}

	blocks := make([]*models.Block, 0)
	err := query.Limit(limit).Find(&blocks).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "get domain blocks error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	// Extract domains
	domains := make([]string, 0, len(blocks))

	for _, b := range blocks {
		if b.TargetDomain != "" {
			domains = append(domains, b.TargetDomain)
		}
	}

	c.JSON(200, domains)
}

// DomainBlockRequest represents the request for blocking a domain.
type DomainBlockRequest struct {
	Domain string `binding:"required" json:"domain"`
}

// BlockDomain handles POST /api/v1/domain_blocks.
func BlockDomain(c *gin.Context) {
	var req DomainBlockRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// Also try form binding
		req.Domain = c.PostForm("domain")
		if req.Domain == "" {
			c.AbortWithStatusJSON(400, gin.H{"error": "domain parameter required"})

			return
		}
	}

	// Parse and validate domain
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "domain cannot be empty"})

		return
	}

	// Extract domain from URL if needed
	if strings.HasPrefix(domain, "http://") || strings.HasPrefix(domain, "https://") {
		parsedURL, err := url.Parse(domain)
		if err != nil {
			c.AbortWithStatusJSON(400, gin.H{"error": "invalid domain"}) //nolint:goconst

			return
		}

		domain = parsedURL.Host
	}

	// Remove port if present
	if idx := strings.Index(domain, ":"); idx != -1 {
		domain = domain[:idx]
	}

	if domain == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid domain"})

		return
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Check if already blocked
	block := &models.Block{}

	err := db.GetDB().Where("account_id = ? AND target_domain = ? AND type = ?", account.Id, domain, models.BlockTypeDomain).First(block).Error
	if err == nil {
		// Already blocked
		c.JSON(200, gin.H{})

		return
	}

	// Create domain block
	now := time.Now()

	block = &models.Block{
		AccountId:    account.Id,
		Type:         models.BlockTypeDomain,
		TargetDomain: domain,
		CreatedAt:    &now,
	}
	if err := db.GetDB().Create(block).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "create domain block error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, gin.H{})
}

// UnblockDomain handles DELETE /api/v1/domain_blocks.
func UnblockDomain(c *gin.Context) {
	var req DomainBlockRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		// Also try form binding
		req.Domain = c.PostForm("domain")
		if req.Domain == "" {
			c.AbortWithStatusJSON(400, gin.H{"error": "domain parameter required"})

			return
		}
	}

	// Parse and validate domain
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "domain cannot be empty"})

		return
	}

	// Extract domain from URL if needed
	if strings.HasPrefix(domain, "http://") || strings.HasPrefix(domain, "https://") {
		parsedURL, err := url.Parse(domain)
		if err != nil {
			c.AbortWithStatusJSON(400, gin.H{"error": "invalid domain"})

			return
		}

		domain = parsedURL.Host
	}

	// Remove port if present
	if idx := strings.Index(domain, ":"); idx != -1 {
		domain = domain[:idx]
	}

	// Get current account
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	// Delete domain block
	err = db.GetDB().Where("account_id = ? AND target_domain = ? AND type = ?", account.Id, domain, models.BlockTypeDomain).Delete(&models.Block{}).Error
	if err != nil {
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"})

		return
	}

	c.JSON(200, gin.H{})
}
