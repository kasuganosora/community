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
	"sync"
	"time"
)

// BridgeFederationDelivery is the shared durable Matrix PDU outbox. The name
// and table are retained for existing rows; native groups never enqueue AP work.
// A per-PDU acknowledgement is required before delivery is complete.
type BridgeFederationDelivery struct {
	Native        bool      `gorm:"not null;default:false"`
	ID            string    `gorm:"primaryKey;size:64"`
	EventID       string    `gorm:"size:255;index"`
	ServerName    string    `gorm:"size:255;index:idx_matrix_outbox_due,priority:3;index:idx_matrix_outbox_peer_due,priority:1"`
	State         string    `gorm:"size:16;index;index:idx_matrix_outbox_due,priority:1;index:idx_matrix_outbox_peer_due,priority:2"`
	LeaseToken    string    `gorm:"size:36"`
	NextAttemptAt time.Time `gorm:"index;index:idx_matrix_outbox_due,priority:2;index:idx_matrix_outbox_peer_due,priority:3"`
	Attempts      int
	LastError     string `gorm:"size:96"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (hs *HomeServer) queueBridgeFederation(tx *gorm.DB, event *MatrixEvent) error {
	return hs.queueMatrixFederation(tx, event, false)
}

func (hs *HomeServer) queueNativeFederation(tx *gorm.DB, event *MatrixEvent) error {
	return hs.queueMatrixFederation(tx, event, true)
}

func (hs *HomeServer) queueMatrixFederation(tx *gorm.DB, event *MatrixEvent, native bool) error {
	var members []MatrixRoomMember
	if err := tx.Select("user_id").Where("room_id = ? AND membership = ?", event.RoomID, MembershipJoin).Find(&members).Error; err != nil {
		return err
	}
	// A departing member's server still needs its kick/ban/leave even if it
	// no longer has any joined members after this state change.
	if native && event.EventType == EventTypeRoomMember && event.StateKey != nil {
		members = append(members, MatrixRoomMember{UserID: *event.StateKey})
	}
	seen := map[string]bool{}
	_, senderServer, _ := ParseMatrixUserID(event.Sender)
	for _, member := range members {
		_, server, ok := ParseMatrixUserID(member.UserID)
		if !ok || server == hs.Config.ServerName || seen[server] || (native && server == senderServer) {
			continue
		}
		seen[server] = true
		sum := sha256.Sum256([]byte(event.EventID + "\x00" + server))
		row := BridgeFederationDelivery{Native: native, ID: hex.EncodeToString(sum[:]), EventID: event.EventID, ServerName: server, State: "pending", NextAttemptAt: time.Now()}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}
func (hs *HomeServer) processBridgeFederationJobs(ctx context.Context) {
	// Pick destinations before events: a backlog from one offline peer must
	// not fill the global batch and hide all work for unrelated healthy peers.
	// Both the peer result and each peer batch are bounded, with no row locks
	// held while doing network requests. The per-event fenced lease remains
	// the cross-process claim authority.
	peers, err := hs.nextFederationPeerBatch(ctx)
	if err != nil || len(peers) == 0 {
		return
	}
	perPeer := min(25, 100/len(peers))
	jobs := make(chan string, len(peers))
	for _, peer := range peers {
		jobs <- peer
	}
	close(jobs)
	var workers sync.WaitGroup
	for i := 0; i < min(4, len(peers)); i++ {
		workers.Go(func() {
			for server := range jobs {
				if ctx.Err() != nil {
					return
				}
				var rows []BridgeFederationDelivery
				if err := db.GetDB().WithContext(ctx).
					Where("server_name = ? AND state IN ? AND next_attempt_at <= ?", server, []string{"pending", "processing"}, time.Now()).
					Order("created_at,id").Limit(perPeer).Find(&rows).Error; err != nil {
					continue
				}
				for _, row := range rows {
					if ctx.Err() != nil {
						return
					}
					if hs.ProcessBridgeFederation(ctx, row.ID) != nil {
						// Do not spend the whole round retrying an offline peer;
						// unclaimed rows remain pending for a later round.
						break
					}
				}
			}
		})
	}
	workers.Wait()
}

// nextFederationPeerBatch rotates the bounded peer window between rounds.
// Sorting only by oldest pending work would continually select the same offline
// peers when they each retain a backlog. A server-name keyset cursor guarantees
// that later destinations get a turn without updating their queued events.
// Each instance owns its cursor; the durable row leases still arbitrate work
// across processes. The mutex protects only these short read queries, never I/O
// to a peer, and the cursor is reconstructible after restart.
func (hs *HomeServer) nextFederationPeerBatch(ctx context.Context) ([]string, error) {
	hs.federationPeerMu.Lock()
	defer hs.federationPeerMu.Unlock()
	cursor := hs.federationPeerCursor
	now := time.Now()
	query := func() *gorm.DB {
		return db.GetDB().WithContext(ctx).Model(&BridgeFederationDelivery{}).
			Where("state IN ? AND next_attempt_at <= ?", []string{"pending", "processing"}, now).
			Group("server_name").Order("server_name")
	}
	var peers []string
	if err := query().Where("server_name > ?", cursor).Limit(16).Pluck("server_name", &peers).Error; err != nil {
		return nil, err
	}
	if cursor != "" && len(peers) < 16 {
		var wrapped []string
		if err := query().Where("server_name <= ? AND server_name <> ?", cursor, "").Limit(16-len(peers)).Pluck("server_name", &wrapped).Error; err != nil {
			return nil, err
		}
		peers = append(peers, wrapped...)
	}
	if len(peers) > 0 {
		hs.federationPeerCursor = peers[len(peers)-1]
	}
	return peers, nil
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
	nextAttempt := time.Now().Add(time.Minute)
	if err != nil {
		state = "pending"
		message = "Matrix federation delivery retry required"
		if row.Native {
			nextAttempt = time.Now().Add(nativeFederationRetryDelay(row.Attempts))
			if !row.CreatedAt.IsZero() && time.Since(row.CreatedAt) >= 7*24*time.Hour {
				state = "failed"
			}
		} else if row.Attempts+1 >= 10 {
			state = "failed"
		}
	}
	result := conn.Model(&BridgeFederationDelivery{}).Where("id = ? AND lease_token = ?", id, lease).Updates(map[string]any{"state": state, "lease_token": "", "last_error": message, "next_attempt_at": nextAttempt, "attempts": gorm.Expr("attempts + 1")})
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
		if redactions > 0 && !row.Native {
			return nil
		} // Never newly disclose an already withdrawn body.
	}
	data, err := buildEventPDU(&event)
	if err != nil {
		return err
	}
	client := newBridgeFederationClient(hs)
	body := map[string]any{"origin": hs.Config.ServerName, "origin_server_ts": event.OriginServerTS, "pdus": []json.RawMessage{data}, "edus": []json.RawMessage{}}
	// Receivers cache transaction responses, including HTTP 200 responses with
	// rejected PDUs. A later delivery attempt needs a fresh transaction ID or
	// a temporary auth/dependency failure can never recover. The fenced claim
	// token is unique even after manual retry; the signed event ID stays stable
	// so receivers still deduplicate the PDU itself after uncertain delivery.
	txnID := "bridge-" + row.ID + "-" + row.LeaseToken
	req, err := client.prepareSignedFederationRequest(ctx, http.MethodPut, client.federationBaseURL(row.ServerName)+"/_matrix/federation/v1/send/"+txnID, row.ServerName, body)
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

func nativeFederationRetryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > 6 {
		attempt = 6
	}
	delay := time.Minute * time.Duration(1<<attempt)
	if delay > time.Hour {
		return time.Hour
	}
	return delay
}

// StartNativeFederation starts the Matrix outbox independently of AP bridging.
func (hs *HomeServer) StartNativeFederation(ctx context.Context) {
	hs.StartBridgeFederation(ctx)
}

func (hs *HomeServer) StartBridgeFederation(ctx context.Context) {
	hs.federationWorkerOnce.Do(func() {
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
	})
}
