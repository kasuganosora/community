package conversation

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
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

	// Newest conversation id first, matching the client's max_id cursor
	// (the id of the last row is the exclusive upper bound of the next page).
	err := db.GetDB().Where("id IN (?)", subQuery).Order("id DESC").Limit(req.Limit).Find(&conversations).Error
	if err != nil {
		err = errors.Wrap(err, "get conversations error")
		_ = c.Error(err)

		return
	}

	ret, err := serializer.ToConversations(conversations, account)
	if err != nil {
		err = errors.Wrap(err, "serializer conversations error")
		_ = c.Error(err)

		return
	}

	c.JSON(200, ret)
}
