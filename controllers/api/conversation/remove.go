package conversation

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"strconv"
)

func Remove(c *gin.Context) {
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

	if err = db.GetDB().Delete(ac).Error; err != nil {
		err = errors.Wrap(err, "can't delete conversation")
		_ = c.Error(err)

		return
	}

	// 删除 Conversation 上面的 userid
	conversation := &models.Conversation{}

	err = db.GetDB().Model(conversation).Where("id = ?", id).First(conversation).Error
	if err != nil {
		err = errors.Wrap(err, "can't find conversation")
		_ = c.Error(err)

		return
	}

	// 删除 Conversation 上面的 userid
	conversation.AccountsIds = utils.Filter(conversation.AccountsIds, func(i uint64) bool {
		return i != account.Id
	})

	if err = db.GetDB().Select("accounts_ids").Save(conversation).Error; err != nil {
		err = errors.Wrap(err, "can't save conversation")
		_ = c.Error(err)

		return
	}

	c.JSON(200, gin.H{})
}
