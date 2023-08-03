package streaming

import (
	"encoding/json"
	"github.com/gorilla/websocket"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"net/http"
)

var upgrade = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

func serializerStatusEvent(account *models.Account, payload string) (ret []byte, err error) {
	type statusEvent struct {
		Type    string         `json:"type"`
		Payload *models.Status `json:"payload"`
	}

	event := &statusEvent{}
	err = json.Unmarshal([]byte(payload), event)
	if err != nil {
		return
	}

	// 查看扩展字段是否加载全, 如果没有加载全, 则加载全
	err = event.Payload.CheckAndLoadAllExtendFields(db.GetDB())
	if err != nil {
		return
	}

	// 序列化
	sStatus, err := serializer.ToStatus(event.Payload, account)

	if err != nil {
		return
	}

	payloadStr, _ := json.Marshal(sStatus)
	retJson := map[string]any{
		"event":   event.Type,
		"payload": string(payloadStr),
	}

	ret, err = json.Marshal(retJson)
	return
}
