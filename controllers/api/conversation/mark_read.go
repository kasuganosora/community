package conversation

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
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

	// Read-through tracks only messages this account is authorized to read.
	var maxStatusId uint64
	err = visibleDirectStatuses(db.GetDB(), account.Id).Where("conversation_id = ?", id).
		Select("COALESCE(MAX(id), 0)").Scan(&maxStatusId).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "can't find readable messages"))
		return
	}
	if maxStatusId == 0 {
		c.JSON(404, gin.H{"error": "conversation has no readable messages"})
		return
	}
	// A concurrent read request must never move the cursor backwards.
	err = db.GetDB().Model(ac).Where("last_status_id < ?", maxStatusId).
		UpdateColumn("last_status_id", maxStatusId).Error
	if err != nil {
		_ = c.Error(errors.Wrap(err, "can't save conversation"))
		return
	}

	c.JSON(200, gin.H{})
}
