package streaming

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/feed/direct"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func DirectStreaming(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)
	timeline := &direct.DirectTimeline{
		Account: account,
	}

	procHandle := func(data any) (event, payload string, err error) {
		conversation := data.(*models.Conversation)
		convs, err := serializer.ToConversation(conversation, account)
		if err != nil {
			return
		}

		payloadBytes, _ := json.Marshal(convs)
		payload = string(payloadBytes)
		event = "conversation"
		return
	}

	startSseService(c, procHandle, timeline)

	return
}
