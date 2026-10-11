package homeserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"io"
	"net/http"
	"time"
)

// BridgeFederationDelivery is limited to bridge-origin events. A remote server
// acknowledgement, including the individual PDU result, is required to finish.
type BridgeFederationDelivery struct {
	ID            string    `gorm:"primaryKey;size:64"`
	EventID       string    `gorm:"size:255;index"`
	ServerName    string    `gorm:"size:255"`
	State         string    `gorm:"size:16;index"`
	LeaseToken    string    `gorm:"size:36"`
	NextAttemptAt time.Time `gorm:"index"`
	Attempts      int
	LastError     string `gorm:"size:96"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (hs *HomeServer) queueBridgeFederation(tx *gorm.DB, event *MatrixEvent) error {
	var members []MatrixRoomMember
	if err := tx.Where("room_id = ? AND membership = ?", event.RoomID, MembershipJoin).Find(&members).Error; err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, member := range members {
		_, server, ok := ParseMatrixUserID(member.UserID)
		if !ok || server == hs.Config.ServerName || seen[server] {
			continue
		}
		seen[server] = true
		sum := sha256.Sum256([]byte(event.EventID + "\x00" + server))
		row := BridgeFederationDelivery{ID: hex.EncodeToString(sum[:]), EventID: event.EventID, ServerName: server, State: "pending", NextAttemptAt: time.Now()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}
func (hs *HomeServer) processBridgeFederationJobs(ctx context.Context) {
	var rows []BridgeFederationDelivery
	if err := db.GetDB().WithContext(ctx).Where("state IN ? AND next_attempt_at <= ?", []string{"pending", "processing"}, time.Now()).Order("created_at,id").Limit(20).Find(&rows).Error; err != nil {
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		_ = hs.ProcessBridgeFederation(ctx, row.ID)
	}
}
func (hs *HomeServer) ProcessBridgeFederation(ctx context.Context, id string) error {
	conn := db.GetDB().WithContext(ctx)
	lease := uuid.NewString()
	claim := conn.Model(&BridgeFederationDelivery{}).Where("id = ? AND state IN ? AND next_attempt_at <= ?", id, []string{"pending", "processing"}, time.Now()).Updates(map[string]any{"state": "processing", "lease_token": lease, "next_attempt_at": time.Now().Add(time.Minute)})
	if claim.Error != nil {
		return claim.Error
	}
	if claim.RowsAffected == 0 {
		return nil
	}
	var row BridgeFederationDelivery
	if err := conn.First(&row, "id = ? AND lease_token = ?", id, lease).Error; err != nil {
		return err
	}
	err := hs.sendBridgeFederation(ctx, &row)
	state, message := "delivered", ""
	if err != nil {
		state = "pending"
		message = "Matrix federation delivery retry required"
		if row.Attempts+1 >= 10 {
			state = "failed"
		}
	}
	result := conn.Model(&BridgeFederationDelivery{}).Where("id = ? AND lease_token = ?", id, lease).Updates(map[string]any{"state": state, "lease_token": "", "last_error": message, "next_attempt_at": time.Now().Add(time.Minute), "attempts": gorm.Expr("attempts + 1")})
	if result.Error != nil {
		return result.Error
	}
	return err
}

var newBridgeFederationClient = NewFederationClient

func (hs *HomeServer) sendBridgeFederation(ctx context.Context, row *BridgeFederationDelivery) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var event MatrixEvent
	if err := db.GetDB().WithContext(ctx).First(&event, "event_id = ?", row.EventID).Error; err != nil {
		return err
	}
	if event.EventType != EventTypeRoomRedaction {
		var redactions int64
		if err := db.GetDB().WithContext(ctx).Model(&MatrixEvent{}).Where("room_id = ? AND redacts = ?", event.RoomID, event.EventID).Count(&redactions).Error; err != nil {
			return err
		}
		if redactions > 0 {
			return nil
		} // Never newly disclose an already withdrawn body.
	}
	data, err := buildEventPDU(&event)
	if err != nil {
		return err
	}
	client := newBridgeFederationClient(hs)
	body := map[string]any{"origin": hs.Config.ServerName, "origin_server_ts": event.OriginServerTS, "pdus": []json.RawMessage{data}, "edus": []json.RawMessage{}}
	req, err := client.prepareSignedFederationRequest(ctx, http.MethodPut, client.federationBaseURL(row.ServerName)+"/_matrix/federation/v1/send/bridge-"+row.ID, row.ServerName, body)
	if err != nil {
		return err
	}
	response, err := client.federationHTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("Matrix federation HTTP failure")
	}
	var result struct {
		PDUs map[string]struct {
			Error string `json:"error"`
		} `json:"pdus"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return err
	}
	pdu, ok := result.PDUs[event.EventID]
	if !ok || pdu.Error != "" {
		return errors.New("Matrix federation did not acknowledge PDU")
	}
	return nil
}

// Callers must authorize the message author/admin before invoking this helper.
// A running lease is never reset by a manual retry.
func RetryBridgeFederationDelivery(tx *gorm.DB, eventID string) error {
	return tx.Model(&BridgeFederationDelivery{}).Where("event_id = ? AND state = ?", eventID, "failed").Updates(map[string]any{"state": "pending", "attempts": 0, "last_error": "", "lease_token": "", "next_attempt_at": time.Now()}).Error
}

// One bounded worker keeps offline peers from delaying the message/AP queues.
func (hs *HomeServer) StartBridgeFederation(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				round, cancel := context.WithTimeout(ctx, 20*time.Second)
				hs.processBridgeFederationJobs(round)
				cancel()
			}
		}
	}()
}
