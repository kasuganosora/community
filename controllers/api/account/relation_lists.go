package account

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// relationPage reads the paging parameters shared by the block, mute,
// domain block and hidden-reblog lists. Lists are newest-first by default
// (Mastodon); order=asc is an extension that serves them oldest-first and
// pages forward with min_id.
type relationPage struct {
	asc   bool
	limit int
}

func readRelationPage(c *gin.Context, defaultLimit int) relationPage {
	p := relationPage{asc: c.Query("order") == "asc", limit: defaultLimit}
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 80 {
			p.limit = parsed
		}
	}

	return p
}

func cursorParam(c *gin.Context, key string) uint64 {
	id, err := strconv.ParseUint(c.Query(key), 10, 64)
	if err != nil {
		return 0
	}

	return id
}

// apply adds ordering, cursor and limit clauses on the id column col.
func (p relationPage) apply(c *gin.Context, q *gorm.DB, col string) *gorm.DB {
	if p.asc {
		q = q.Order(col + " ASC")
		if after := max(cursorParam(c, "min_id"), cursorParam(c, "since_id")); after > 0 {
			q = q.Where(col+" > ?", after)
		}
	} else {
		q = q.Order(col + " DESC")
		if before := cursorParam(c, "max_id"); before > 0 {
			q = q.Where(col+" < ?", before)
		}
		if after := max(cursorParam(c, "min_id"), cursorParam(c, "since_id")); after > 0 {
			q = q.Where(col+" > ?", after)
		}
	}

	return q.Limit(p.limit)
}

// links writes the Link header for a page whose cursor ids are ids.
func (p relationPage) links(c *gin.Context, ids []uint64) {
	if len(ids) == 0 {
		return
	}

	hasNext := len(ids) >= p.limit
	if p.asc {
		common.SetAscPageLinks(c, ids[len(ids)-1], hasNext)

		return
	}

	common.SetPageLinksUint(c, ids[0], ids[len(ids)-1], hasNext)
}

// accountsInOrder loads the accounts for targetIDs and returns them in that
// order, skipping any that no longer exist.
func accountsInOrder(targetIDs []uint64) ([]*serializer.Account, error) {
	result := make([]*serializer.Account, 0, len(targetIDs))
	if len(targetIDs) == 0 {
		return result, nil
	}

	accounts := make([]*models.Account, 0, len(targetIDs))
	if err := db.GetDB().Preload("Emojis").Preload("Extra").Where("id IN ?", targetIDs).Find(&accounts).Error; err != nil {
		return nil, errors.Wrap(err, "get accounts error")
	}

	byID := make(map[uint64]*models.Account, len(accounts))
	for _, acc := range accounts {
		byID[acc.Id] = acc
	}

	for _, id := range targetIDs {
		if acc, ok := byID[id]; ok {
			result = append(result, serializer.ToAccount(acc))
		}
	}

	return result, nil
}

// HiddenReblogsList handles GET /api/v1/hidden_reblogs: the accounts the
// current user follows with reblogs turned off (Mastodon's
// "show_reblogs=false"), newest follow first. Not part of the Mastodon API;
// Mastodon clients get the same information from relationships.
func HiddenReblogsList(c *gin.Context) {
	page := readRelationPage(c, 40)
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	conn := db.GetDB()
	query := conn.Model(&models.Follow{}).
		Where("follows.account_id = ? AND follows.status = ?", account.Id, models.FollowStatusFollowed).
		Where(models.HiddenReblogsCondition(conn))

	follows := make([]*models.Follow, 0)
	if err := page.apply(c, query, "follows.id").Find(&follows).Error; err != nil {
		_ = c.Error(errors.Wrap(err, "get hidden reblogs error"))
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})

		return
	}

	cursors := make([]uint64, 0, len(follows))
	targets := make([]uint64, 0, len(follows))
	for _, f := range follows {
		cursors = append(cursors, f.Id)
		targets = append(targets, f.FollowId)
	}

	result, err := accountsInOrder(targets)
	if err != nil {
		_ = c.Error(err)
		c.AbortWithStatusJSON(500, gin.H{"error": "internal error", "code": "INTERNAL_ERROR"})

		return
	}

	page.links(c, cursors)
	c.JSON(200, result)
}
