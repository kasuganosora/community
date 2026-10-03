package homeserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type directToDeviceEDU struct {
	Sender    string                               `json:"sender"`
	Type      string                               `json:"type"`
	MessageID string                               `json:"message_id"`
	Messages  map[string]map[string]map[string]any `json:"messages"`
}

// A replay marker survives /sync consuming the actual inbox messages.
type MatrixToDeviceDelivery struct {
	ID        string    `gorm:"primaryKey;size:64"`
	CreatedAt time.Time `gorm:"autoCreateTime"`
}

func toDeviceID(parts ...string) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func deliverLocalToDevice(hs *HomeServer, origin string, edu directToDeviceEDU) error {
	_, domain, ok := ParseMatrixUserID(edu.Sender)
	if !ok || domain != origin || edu.Type == "" || edu.MessageID == "" {
		return fmt.Errorf("invalid direct-to-device sender, type or message ID")
	}
	notifications := map[string]map[string]bool{}
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		marker := MatrixToDeviceDelivery{ID: toDeviceID(origin, edu.Sender, edu.MessageID)}
		inserted := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&marker)
		if inserted.Error != nil {
			return inserted.Error
		}
		if inserted.RowsAffected == 0 {
			return nil
		}
		for user, devices := range edu.Messages {
			_, server, ok := ParseMatrixUserID(user)
			if !ok || server != hs.Config.ServerName {
				continue
			}
			notifications[user] = map[string]bool{}
			for device, content := range devices {
				targets := []string{device}
				if device == "*" {
					var sessions []MatrixUserSession
					if err := tx.Where("user_id = ?", user).Find(&sessions).Error; err != nil {
						return err
					}
					targets = nil
					seen := map[string]bool{}
					for _, session := range sessions {
						if !seen[session.DeviceID] {
							targets = append(targets, session.DeviceID)
							seen[session.DeviceID] = true
						}
					}
				}
				for _, target := range targets {
					message := MatrixToDeviceMessage{Sender: edu.Sender, TargetUser: user, TargetDevice: target, EventType: edu.Type, Content: content}
					if err := tx.Create(&message).Error; err != nil {
						return err
					}
					notifications[user][target] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for user, devices := range notifications {
		for device := range devices {
			hs.NotifyToDeviceUpdate(user, device)
		}
	}
	return nil
}

func sendClientToDevice(hs *HomeServer, sender, device, eventType, txnID string, messages map[string]map[string]map[string]any) error {
	groups := map[string]map[string]map[string]map[string]any{}
	for user, devices := range messages {
		_, server, ok := ParseMatrixUserID(user)
		if !ok {
			return fmt.Errorf("invalid target user ID")
		}
		if groups[server] == nil {
			groups[server] = map[string]map[string]map[string]any{}
		}
		groups[server][user] = devices
	}
	id := toDeviceID("client", sender, device, eventType, txnID)
	client := NewFederationClient(hs)
	for server, targets := range groups {
		edu := directToDeviceEDU{Sender: sender, Type: eventType, MessageID: id, Messages: targets}
		if server == hs.Config.ServerName {
			if err := deliverLocalToDevice(hs, server, edu); err != nil {
				return err
			}
			continue
		}
		if err := sendDirectToDeviceEDU(client, server, edu); err != nil {
			return err
		}
	}
	return nil
}

func sendDirectToDeviceEDU(client *FederationClient, server string, edu directToDeviceEDU) error {
	raw, err := json.Marshal(map[string]any{"edu_type": "m.direct_to_device", "content": edu})
	if err != nil {
		return err
	}
	return client.SendTransaction(server, "td-"+edu.MessageID, nil, []json.RawMessage{raw})
}
