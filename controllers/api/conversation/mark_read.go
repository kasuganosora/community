package conversation

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"strconv"
)

func MarkRead(c *gin.Context) {
	idStr := c.Param("id")
	if idStr == "" {
		err := errors.New("id is empty")
		_ = c.Error(err)

		return
	}

	id, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		err = errors.Wrap(err, "id must be number")
		_ = c.Error(err)

		return
	}

	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

	ac := &models.AccountConversation{}

	err = db.GetDB().Model(ac).Where("account_id = ? AND conversation_id = ?", account.Id, id).First(ac).Error
	if err != nil {
		err = errors.Wrap(err, "can't find conversation")
		_ = c.Error(err)

		return
	}

	conversation := &models.Conversation{}

	err = db.GetDB().Model(conversation).Where("id = ?", id).First(conversation).Error
	if err != nil {
		err = errors.Wrap(err, "can't find conversation")
		_ = c.Error(err)

		return
	}

	// 获取这个 conversation 最大的 status id
	maxStatusId := utils.Max(conversation.StatusesIds)

	// 写入到 account conversation
	ac.LastStatusId = maxStatusId

	if err = db.GetDB().Select("last_status_id").Save(ac).Error; err != nil {
		err = errors.Wrap(err, "can't save conversation")
		_ = c.Error(err)

		return
	}

	c.JSON(200, gin.H{})
}
