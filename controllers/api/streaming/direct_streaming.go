package streaming

import (
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed/direct"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
)

func DirectStreaming(c *gin.Context) {
	account := controllers.GetAccount(c)
	timeline := &direct.DirectTimeline{
		Account: account,
	}

	procHandle := func(data any) (string, string, error) {
		payload, ok := data.(string)
		if !ok {
			return "", "", errEventNotVisible
		}
		return serializerConversationEvent(account, payload)
	}

	startSseService(c, procHandle, timeline)
}

// Redis publishes a JSON envelope, not an in-process *Conversation pointer.
func serializerConversationEvent(account *models.Account, payload string) (string, string, error) {
	var event struct {
		Payload *models.Conversation `json:"payload"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return "", "", err
	}
	if event.Payload == nil || event.Payload.Id == 0 {
		return "", "", errEventNotVisible
	}
	account, err := currentStreamingAccount(account)
	if err != nil {
		return "", "", err
	}
	if account == nil {
		return "", "", errEventNotVisible
	}
	var current models.Conversation
	if err := db.GetDB().First(&current, event.Payload.Id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", "", errEventNotVisible
		}
		return "", "", err
	}
	// This serializer selects only presently visible messages and participants;
	// the old union of AccountsIds/StatusesIds is not an authorization grant.
	conversation, err := serializer.ToConversation(&current, account)
	if err != nil {
		return "", "", err
	}
	encoded, err := json.Marshal(conversation)
	return "conversation", string(encoded), err
}
