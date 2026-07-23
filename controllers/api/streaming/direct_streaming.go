package streaming

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/feed/direct"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
)

func DirectStreaming(c *gin.Context) {
	account := controllers.GetAccount(c)
	timeline := &direct.DirectTimeline{
		Account: account,
	}

	procHandle := func(data any) (event, payload string, err error) {
		conversation := data.(*models.Conversation) //nolint:forcetypeassert

		convs, err := serializer.ToConversation(conversation, account)
		if err != nil {
			return
		}

		payloadBytes, err := json.Marshal(convs)
		if err != nil {
			return
		}
		payload = string(payloadBytes)
		event = "conversation"

		return
	}

	startSseService(c, procHandle, timeline)
}
