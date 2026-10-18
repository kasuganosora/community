package account

import (
	"github.com/kasuganosora/community/controllers"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

// DomainBlocksList handles GET /api/v1/domain_blocks.
func DomainBlocksList(c *gin.Context) {
	page := readRelationPage(c, 80)
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	query := db.GetDB().Model(&models.Block{}).
		Where("account_id = ? AND type = ?", account.Id, models.BlockTypeDomain)

	blocks := make([]*models.Block, 0)
	if err := page.apply(c, query, "id").Find(&blocks).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get domain blocks error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst

		return
	}

	domains := make([]string, 0, len(blocks))
	cursors := make([]uint64, 0, len(blocks))
	for _, b := range blocks {
		cursors = append(cursors, b.Id)
		if b.TargetDomain != "" {
			domains = append(domains, b.TargetDomain)
		}
	}

	page.links(c, cursors)
	c.JSON(200, domains)
}

// Stable error codes for domain block requests.
const (
	ErrCodeDomainRequired = "DOMAIN_REQUIRED"
	ErrCodeDomainInvalid  = "DOMAIN_INVALID"
)

// validBlockDomain accepts a bare host name such as "example.com" or an
// IDN like "例子.测试": at least one dot, no empty labels and nothing that
// belongs to a URL or an acct (spaces, slashes, @, ...).
func validBlockDomain(domain string) bool {
	if domain == "" || len(domain) > 253 || !strings.Contains(domain, ".") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}

	return !strings.ContainsAny(domain, " \t\r\n/@?#\\:*,;")
}

// DomainBlockRequest represents the request for blocking a domain.
type DomainBlockRequest struct {
	Domain string `binding:"required" json:"domain"`
}

// BlockDomain handles POST /api/v1/domain_blocks.
func BlockDomain(c *gin.Context) {
	var req DomainBlockRequest
	if err := controllers.BindBody(c, &req); err != nil {
		// Also try form binding
		req.Domain = c.PostForm("domain")
		if req.Domain == "" {
			c.AbortWithStatusJSON(400, gin.H{"error": "domain parameter required", "code": ErrCodeDomainRequired})

			return
		}
	}

	// Parse and validate domain
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "domain cannot be empty", "code": ErrCodeDomainRequired})

		return
	}

	// Extract domain from URL if needed
	if strings.HasPrefix(domain, "http://") || strings.HasPrefix(domain, "https://") {
		parsedURL, err := url.Parse(domain)
		if err != nil {
			c.AbortWithStatusJSON(400, gin.H{"error": "invalid domain", "code": ErrCodeDomainInvalid}) //nolint:goconst

			return
		}

		domain = parsedURL.Host
	}

	// Remove port if present
	if idx := strings.Index(domain, ":"); idx != -1 {
		domain = domain[:idx]
	}

	if !validBlockDomain(domain) {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid domain", "code": ErrCodeDomainInvalid})

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
	err := controllers.BindBody(c, &req)
	if err != nil {
		// Also try form binding
		req.Domain = c.PostForm("domain")
		if req.Domain == "" {
			c.AbortWithStatusJSON(400, gin.H{"error": "domain parameter required", "code": ErrCodeDomainRequired})

			return
		}
	}

	// Parse and validate domain
	domain := strings.ToLower(strings.TrimSpace(req.Domain))
	if domain == "" {
		c.AbortWithStatusJSON(400, gin.H{"error": "domain cannot be empty", "code": ErrCodeDomainRequired})

		return
	}

	// Extract domain from URL if needed
	if strings.HasPrefix(domain, "http://") || strings.HasPrefix(domain, "https://") {
		parsedURL, err := url.Parse(domain)
		if err != nil {
			c.AbortWithStatusJSON(400, gin.H{"error": "invalid domain", "code": ErrCodeDomainInvalid})

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
