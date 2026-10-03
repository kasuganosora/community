package conversation

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"strconv"
)

type ListRequest struct {
	MinId   string `form:"min_id"`
	MaxId   string `form:"max_id"`
	Limit   int    `form:"limit"`
	SinceId string `form:"since_id"`
}

func List(c *gin.Context) {
	req := &ListRequest{}
	if err := c.ShouldBindQuery(req); err != nil {
		c.JSON(422, gin.H{
			"error": err.Error(),
		})

		return
	}

	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	conversations := make([]*models.Conversation, 0)
	subQuery := db.GetDB().Model(&models.AccountConversation{}).Select("conversation_id").Where("account_id = ?", account.Id)

	if req.MinId != "" {
		minId, err := strconv.ParseUint(req.MinId, 10, 64)
		if err != nil {
			err = errors.Wrap(err, "min_id must be number")
			_ = c.Error(err)

			return
		}

		subQuery = subQuery.Where("conversation_id > ?", minId)
	}

	if req.MaxId != "" {
		maxId, err := strconv.ParseUint(req.MaxId, 10, 64)
		if err != nil {
			err = errors.Wrap(err, "max_id must be number")
			_ = c.Error(err)

			return
		}

		subQuery = subQuery.Where("conversation_id < ?", maxId)
	}

	if req.SinceId != "" {
		sinceId, err := strconv.ParseUint(req.SinceId, 10, 64)
		if err != nil {
			err = errors.Wrap(err, "since_id must be number")
			_ = c.Error(err)

			return
		}

		subQuery = subQuery.Where("conversation_id >= ?", sinceId)
	}

	if req.Limit == 0 {
		req.Limit = 20
	}

	// Public replies also have conversations for threading. Only return threads
	// with direct messages visible to this account, filtering before pagination.
	directStatuses := visibleDirectStatuses(db.GetDB(), account.Id)

	// Newest conversation id first, matching the client's max_id cursor
	// (the id of the last row is the exclusive upper bound of the next page).
	err := db.GetDB().Where("id IN (?)", subQuery).Where("id IN (?)", directStatuses.Select("conversation_id")).Order("id DESC").Limit(req.Limit).Find(&conversations).Error
	if err != nil {
		err = errors.Wrap(err, "get conversations error")
		_ = c.Error(err)

		return
	}

	// A thread may contain public replies or messages addressed to a different
	// participant. Its preview must be the newest direct message visible to us.
	if len(conversations) > 0 {
		ids := make([]uint64, 0, len(conversations))
		for _, conversation := range conversations {
			ids = append(ids, conversation.Id)
		}
		var previews []struct {
			ConversationId uint64
			LastStatusId   uint64
		}
		err = visibleDirectStatuses(db.GetDB(), account.Id).
			Select("conversation_id, MAX(id) AS last_status_id").
			Where("conversation_id IN ?", ids).Group("conversation_id").Scan(&previews).Error
		if err != nil {
			_ = c.Error(errors.Wrap(err, "get direct conversation previews error"))
			return
		}
		latest := make(map[uint64]uint64, len(previews))
		for _, preview := range previews {
			latest[preview.ConversationId] = preview.LastStatusId
		}
		for _, conversation := range conversations {
			conversation.StatusesIds = []uint64{latest[conversation.Id]}
		}
	}

	ret, err := serializer.ToConversations(conversations, account)
	if err != nil {
		err = errors.Wrap(err, "serializer conversations error")
		_ = c.Error(err)

		return
	}

	c.JSON(200, ret)
}

// Membership in a reply thread does not grant access to every direct message.
func visibleDirectStatuses(conn *gorm.DB, accountId uint64) *gorm.DB {
	recipients := conn.Table("status_to_accounts").Select("status_id").Where("account_id = ?", accountId)
	return conn.Model(&models.Status{}).
		Where("visibility = ? AND deleted_at IS NULL AND scheduled_at IS NULL", models.StatusVisibilityDirect).
		Where("account_id = ? OR id IN (?)", accountId, recipients)
}
