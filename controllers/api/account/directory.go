package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	directoryDefaultLimit = 40
	directoryMaxLimit     = 80
)

// GetDirectory handles GET /api/v1/directory.
//
// Mirrors Mastodon: only discoverable accounts are listed; suspended
// accounts, local accounts whose user is not active (unapproved, rejected,
// unconfirmed or banned) and accounts on suspended or silenced instances are
// left out. order=active (default) sorts by most recent status, order=new by
// newest account; local=true limits the list to local accounts. A signed-in
// viewer does not see accounts they block, are blocked by or mute, nor
// accounts on domains they block.
func GetDirectory(c *gin.Context) {
	limit := directoryDefaultLimit
	if parsed, err := strconv.Atoi(c.Query("limit")); err == nil && parsed > 0 {
		limit = min(parsed, directoryMaxLimit)
	}

	offset := 0
	if parsed, err := strconv.Atoi(c.Query("offset")); err == nil && parsed >= 0 {
		offset = parsed
	}

	conn := db.GetDB().WithContext(c.Request.Context())

	activeUsers := conn.Model(&models.User{}).Select("id").
		Where("status = ? AND is_ban = ?", models.UserStatusNormal, false)

	query := conn.Model(&models.Account{}).
		Where("accounts.discoverable = ?", true).
		Where("accounts.is_ban = ?", false).
		Where("(accounts.domain IS NOT NULL OR accounts.user_id IN (?))", activeUsers)

	if parseBoolQuery(c.Query("local")) {
		query = query.Where("accounts.domain IS NULL")
	} else {
		suspend, silence, err := models.DomainSeverities(conn)
		if err != nil {
			directoryError(c, err)

			return
		}

		if hidden := domainSetUnion(suspend, silence); len(hidden) > 0 {
			query = query.Where("(accounts.domain IS NULL OR LOWER(accounts.domain) NOT IN ?)", hidden)
		}
	}

	if viewer := directoryViewer(c); viewer != nil {
		query = excludeForViewer(conn, query, viewer)
	}

	if c.Query("order") == "new" {
		query = query.Order("accounts.created_at DESC").Order("accounts.id DESC")
	} else {
		// "active": most recent status first, accounts that never posted last.
		// "IS NULL" sorts false before true on MySQL, PostgreSQL and SQLite,
		// which gives NULLS LAST portably.
		query = query.Order("accounts.last_status_at IS NULL").
			Order("accounts.last_status_at DESC").
			Order("accounts.id DESC")
	}

	accounts := make([]*models.Account, 0)

	err := query.Preload("Emojis").Preload("Extra").
		Limit(limit).Offset(offset).Find(&accounts).Error
	if err != nil {
		directoryError(c, err)

		return
	}

	common.SetOffsetLinks(c, offset, len(accounts), limit)
	c.JSON(200, serializer.ToAccounts(accounts))
}

func directoryError(c *gin.Context, err error) {
	_ = c.Error(errors.Wrap(err, "get directory error"))
	c.AbortWithStatusJSON(500, gin.H{"error": "internal error"}) //nolint:goconst
}

func directoryViewer(c *gin.Context) *models.Account {
	value, ok := c.Get("account")
	if !ok {
		return nil
	}

	viewer, _ := value.(*models.Account)
	if viewer == nil || viewer.Id == 0 {
		return nil
	}

	return viewer
}

func excludeForViewer(conn *gorm.DB, query *gorm.DB, viewer *models.Account) *gorm.DB {
	blocking := conn.Model(&models.Block{}).Select("target_account_id").
		Where("account_id = ? AND type = ?", viewer.Id, models.BlockTypeAccount)
	blockedBy := conn.Model(&models.Block{}).Select("account_id").
		Where("target_account_id = ? AND type = ?", viewer.Id, models.BlockTypeAccount)
	muting := conn.Model(&models.MuteAccount{}).Select("target_id").
		Where("account_id = ? AND (expired_at IS NULL OR expired_at > ?)", viewer.Id, time.Now())
	blockedDomains := conn.Model(&models.Block{}).Select("LOWER(target_domain)").
		Where("account_id = ? AND type = ? AND target_domain IS NOT NULL", viewer.Id, models.BlockTypeDomain)

	return query.
		Where("accounts.id NOT IN (?)", blocking).
		Where("accounts.id NOT IN (?)", blockedBy).
		Where("accounts.id NOT IN (?)", muting).
		Where("(accounts.domain IS NULL OR LOWER(accounts.domain) NOT IN (?))", blockedDomains)
}

func domainSetUnion(sets ...map[string]bool) []string {
	seen := map[string]bool{}
	out := make([]string, 0)

	for _, set := range sets {
		for domain, listed := range set {
			domain = strings.ToLower(domain)
			if listed && !seen[domain] {
				seen[domain] = true
				out = append(out, domain)
			}
		}
	}

	sort.Strings(out)

	return out
}

func parseBoolQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "t", "on", "yes":
		return true
	}

	return false
}
