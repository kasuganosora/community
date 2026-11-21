package conversation

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
)

func MarkRead(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}

	account := controllers.MustGetAccount(c)
	conn := db.GetDB().WithContext(c.Request.Context())
	var membership models.AccountConversation
	err = conn.Where("account_id = ? AND conversation_id = ?", account.Id, id).First(&membership).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation not found"})
		return
	}
	if err != nil {
		_ = c.Error(err)
		return
	}

	// A thread can contain several audiences. Advance through the exact
	// message rendered for this account, just as it appeared in the inbox.
	reply, err := serializer.ToConversation(&models.Conversation{Id: id}, account)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "conversation has no readable messages"})
		return
	}
	if err != nil {
		_ = c.Error(err)
		return
	}
	lastID, err := strconv.ParseUint(reply.LastStatus.Id, 10, 64)
	if err != nil {
		_ = c.Error(err)
		return
	}
	// A concurrent read request must never move the cursor backwards.
	if err := conn.Model(&membership).Where("last_status_id IS NULL OR last_status_id < ?", lastID).UpdateColumn("last_status_id", lastID).Error; err != nil {
		_ = c.Error(err)
		return
	}
	reply.Unread = false
	c.JSON(http.StatusOK, reply)
}
