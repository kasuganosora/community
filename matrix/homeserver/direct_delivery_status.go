package homeserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DirectDeliveryStatus reports transport acceptance, never a read receipt.
// No remote URL, payload, exception text, or recipient identity is exposed.
type DirectDeliveryStatus struct {
	EventID        string    `json:"event_id"`
	RoomID         string    `json:"room_id"`
	State          string    `json:"state"`
	RemoteReceived bool      `json:"remote_received"`
	ErrorCode      string    `json:"error_code,omitempty"`
	CanRetry       bool      `json:"can_retry"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (hs *HomeServer) directDeliveryJob(tx *gorm.DB, accountID uint64, roomID, eventID string, lockJob ...bool) (*MatrixDMOutbound, error) {
	// Lock only the job row. Propagating FOR UPDATE into audience COUNT queries
	// is invalid in PostgreSQL and needlessly broadens the lock order.
	jobQuery := tx.Session(&gorm.Session{})
	if len(lockJob) > 0 && lockJob[0] {
		jobQuery = jobQuery.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var job MatrixDMOutbound
	if err := jobQuery.Where("event_id = ? AND room_id = ? AND sender_account_id = ?", eventID, roomID, accountID).First(&job).Error; err != nil {
		return nil, err
	}
	var event MatrixEvent
	if err := tx.Where("event_id = ? AND room_id = ?", eventID, roomID).First(&event).Error; err != nil {
		return nil, err
	}
	if _, _, err := hs.outboundDirectAudience(tx, &job, &event); err != nil {
		return nil, err
	}
	return &job, nil
}

func loadDirectDeliveryRows(tx *gorm.DB, accountID uint64, ids []uint64) (map[uint64][]models.FederationOutbox, map[string][]activitypub.Delivery, string, error) {
	byStatus := map[uint64][]models.FederationOutbox{}
	byActivity := map[string][]activitypub.Delivery{}
	var author models.Account
	if err := tx.First(&author, accountID).Error; err != nil {
		return nil, nil, "", err
	}
	actorURI := author.ActorURI(systemsettings.GetBaseURL())
	if len(ids) == 0 {
		return byStatus, byActivity, actorURI, nil
	}
	var events []models.FederationOutbox
	if err := tx.Select("id", "object_id", "recipient_ids", "state", "updated_at").Where("account_id = ? AND object_id IN ? AND kind = ?", accountID, ids, "Create").Find(&events).Error; err != nil {
		return nil, nil, "", err
	}
	activityIDs := []string{}
	for _, event := range events {
		byStatus[event.ObjectID] = append(byStatus[event.ObjectID], event)
		activityIDs = append(activityIDs, fmt.Sprintf("%s/activities/%d", actorURI, event.ID))
	}
	if len(activityIDs) > 0 {
		var deliveries []activitypub.Delivery
		if err := tx.Select("activity_id", "status", "last_error", "updated_at").Where("activity_id IN ?", activityIDs).Find(&deliveries).Error; err != nil {
			return nil, nil, "", err
		}
		for _, d := range deliveries {
			byActivity[d.ActivityID] = append(byActivity[d.ActivityID], d)
		}
	}
	return byStatus, byActivity, actorURI, nil
}
func directDeliveryAggregate(tx *gorm.DB, job *MatrixDMOutbound) (DirectDeliveryStatus, error) {

	events, deliveries, actor, err := loadDirectDeliveryRows(tx, job.SenderAccountID, []uint64{job.StatusID})
	if err != nil {
		return DirectDeliveryStatus{}, err
	}
	result, err := directDeliveryFromRows(job, events[job.StatusID], deliveries, actor)
	if err != nil {
		return result, err
	}
	matrix, err := loadDirectMatrixDeliveryRows(tx, []string{job.EventID})
	if err != nil {
		return result, err
	}
	return mergeDirectMatrixDelivery(result, matrix[job.EventID]), nil
}

// BatchDirectDeliveryStatuses validates current room access once and batches
// transport rows. Callers must supply at most one bounded message page.
func (hs *HomeServer) BatchDirectDeliveryStatuses(ctx context.Context, accountID uint64, roomID string, eventIDs []string) (map[string]DirectDeliveryStatus, error) {
	result := map[string]DirectDeliveryStatus{}
	if len(eventIDs) == 0 {
		return result, nil
	}
	if len(eventIDs) > 100 {
		return nil, errors.New("delivery page too large")
	}
	tx := db.GetDB().WithContext(ctx)
	var jobs []MatrixDMOutbound
	if err := tx.Where("room_id = ? AND sender_account_id = ? AND event_id IN ?", roomID, accountID, eventIDs).Find(&jobs).Error; err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return result, nil
	}
	// Reading one's own transport metadata is different from resending an
	// old audience. Membership changes must not make a whole message page fail.
	var viewer models.Account
	if err := tx.Preload("User").First(&viewer, accountID).Error; err != nil {
		return nil, err
	}
	if viewer.IsBan {
		return nil, errDirectMessageSkipped
	}
	userID := hs.bridgeAccountID(viewer)
	if viewer.UserId == 0 {
		var external MatrixExternalActor
		if err := tx.Where("account_id = ?", accountID).First(&external).Error; err != nil {
			return nil, err
		}
		userID = external.MatrixID
	} else if viewer.User == nil || viewer.User.IsBan || viewer.User.Status != models.UserStatusNormal {
		return nil, errDirectMessageSkipped
	}
	var joined int64
	if err := tx.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ? AND membership = ?", roomID, userID, MembershipJoin).Count(&joined).Error; err != nil {
		return nil, err
	}
	if joined != 1 {
		return nil, errDirectMessageSkipped
	}
	ids := []uint64{}
	for _, job := range jobs {
		if job.StatusID != 0 {
			ids = append(ids, job.StatusID)
		}
	}
	events, deliveries, actor, err := loadDirectDeliveryRows(tx, accountID, ids)
	if err != nil {
		return nil, err
	}
	matrix, err := loadDirectMatrixDeliveryRows(tx, eventIDs)
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		state, err := directDeliveryFromRows(&job, events[job.StatusID], deliveries, actor)
		if err != nil {
			return nil, err
		}
		state = mergeDirectMatrixDelivery(state, matrix[job.EventID])
		if state.CanRetry {
			if _, err := hs.directDeliveryJob(tx, accountID, roomID, job.EventID); err != nil {
				if !errors.Is(err, errDirectMessageSkipped) && !errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, err
				}
				state.CanRetry = false
			}
		}
		result[job.EventID] = state
	}
	return result, nil
}

func directDeliveryFromRows(job *MatrixDMOutbound, outbox []models.FederationOutbox, byActivity map[string][]activitypub.Delivery, actorURI string) (DirectDeliveryStatus, error) {
	result := DirectDeliveryStatus{EventID: job.EventID, RoomID: job.RoomID, State: "queued", UpdatedAt: job.UpdatedAt}
	switch job.State {
	case "failed":
		result.State = "failed"
		result.ErrorCode = "bridge_failed"
		result.CanRetry = true
		return result, nil
	case "skipped", "cancelled":
		result.State = "skipped"
		return result, nil
	case "processing":
		result.State = "sending"
		return result, nil
	}
	if job.StatusID == 0 {
		return result, nil
	}
	if len(outbox) == 0 {
		result.State = "skipped"
		return result, nil
	}
	pending, sending, failed, skipped, received := false, false, false, false, false
	for _, o := range outbox {
		if o.UpdatedAt.After(result.UpdatedAt) {
			result.UpdatedAt = o.UpdatedAt
		}
		switch o.State {
		case "failed":
			failed = true
			continue
		case "pending":
			pending = true
			continue
		case "processing":
			sending = true
			continue
		case "cancelled", "skipped":
			skipped = true
			continue
		case "processed":
		default:
			pending = true
			continue
		}
		id := fmt.Sprintf("%s/activities/%d", actorURI, o.ID)
		deliveries := byActivity[id]
		if len(deliveries) == 0 && len(o.RecipientIDs) > 0 {
			skipped = true
		}
		for _, d := range deliveries {
			if d.UpdatedAt != nil && d.UpdatedAt.After(result.UpdatedAt) {
				result.UpdatedAt = *d.UpdatedAt
			}
			switch d.Status {
			case activitypub.DeliveryStatusFailed:
				failed = true
			case activitypub.DeliveryStatusProcessing:
				sending = true
			case activitypub.DeliveryStatusPending:
				pending = true
			case activitypub.DeliveryStatusDelivered:
				if d.LastError != "" {
					skipped = true
				} else {
					received = true
				}
			default:
				skipped = true
			}
		}
	}
	switch {
	case failed:
		result.State = "failed"
		result.ErrorCode = "federation_delivery_failed"
		result.CanRetry = true
	case sending:
		result.State = "sending"
	case pending:
		result.State = "queued"
	case skipped:
		result.State = "skipped"
	default:
		result.State = "delivered"
		result.RemoteReceived = received
	}
	return result, nil
}

func (hs *HomeServer) GetDirectDeliveryStatus(ctx context.Context, accountID uint64, roomID, eventID string) (DirectDeliveryStatus, error) {
	tx := db.GetDB().WithContext(ctx)
	job, err := hs.directDeliveryJob(tx, accountID, roomID, eventID)
	if err != nil {
		return DirectDeliveryStatus{}, err
	}
	return directDeliveryAggregate(tx, job)
}

// RetryDirectDelivery resets only failed existing rows. A successful/leased
// transport row is never replayed and the original Activity ID is retained.
func (hs *HomeServer) RetryDirectDelivery(ctx context.Context, accountID uint64, roomID, eventID string) (DirectDeliveryStatus, error) {
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		job, err := hs.directDeliveryJob(tx, accountID, roomID, eventID, true)
		if err != nil {
			return err
		}
		var failedMatrix int64
		if err := tx.Model(&BridgeFederationDelivery{}).Where("event_id = ? AND state = ?", eventID, "failed").Count(&failedMatrix).Error; err != nil {
			return err
		}
		if failedMatrix > 0 {
			if err := RetryBridgeFederationDelivery(tx, eventID); err != nil {
				return err
			}
		}
		if job.StatusID == 0 {
			if job.State != "failed" {
				if failedMatrix > 0 {
					return tx.Model(&MatrixDMOutbound{}).Where("event_id = ?", eventID).Update("delivery_feedback_state", "").Error
				}
				return nil
			}
			return tx.Model(&MatrixDMOutbound{}).Where("event_id = ? AND state = ?", eventID, "failed").Updates(map[string]any{"state": "pending", "attempts": 0, "last_error": "", "next_attempt_at": time.Now(), "delivery_feedback_state": ""}).Error
		}
		var status models.Status
		if err := tx.Where("id = ? AND account_id = ? AND visibility = ? AND deleted_at IS NULL", job.StatusID, accountID, models.StatusVisibilityDirect).First(&status).Error; err != nil {
			return err
		}
		if status.LocalOnly {
			return errors.New("local-only message cannot be retried remotely")
		}
		var author models.Account
		if err := tx.First(&author, accountID).Error; err != nil {
			return err
		}
		var events []models.FederationOutbox
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("object_id = ? AND account_id = ? AND kind = ?", status.Id, accountID, "Create").Find(&events).Error; err != nil {
			return err
		}
		changed := failedMatrix > 0
		for _, event := range events {
			if event.State == "failed" {
				update := tx.Model(&models.FederationOutbox{}).Where("id = ? AND state = ?", event.ID, "failed").Updates(map[string]any{"state": "pending", "attempts": 0, "last_error": "", "next_attempt_at": time.Now()})
				if update.Error != nil {
					return update.Error
				}
				changed = changed || update.RowsAffected > 0
			}
			id := fmt.Sprintf("%s/activities/%d", author.ActorURI(systemsettings.GetBaseURL()), event.ID)
			update := tx.Model(&activitypub.Delivery{}).Where("activity_id = ? AND status = ?", id, activitypub.DeliveryStatusFailed).Updates(map[string]any{"status": activitypub.DeliveryStatusPending, "retries": 0, "last_error": "", "next_retry_at": time.Now()})
			if update.Error != nil {
				return update.Error
			}
			changed = changed || update.RowsAffected > 0
		}
		if changed {
			return tx.Model(&MatrixDMOutbound{}).Where("event_id = ?", eventID).Update("delivery_feedback_state", "").Error
		}
		return nil
	})
	if err != nil {
		return DirectDeliveryStatus{}, err
	}
	return hs.GetDirectDeliveryStatus(ctx, accountID, roomID, eventID)
}

// publishDirectDeliveryFeedback uses private per-user room account data, not a
// room message. Peers never see delivery failures or author retry controls.
// Checkpoints live on the existing bridge job; this is not another queue.
func (hs *HomeServer) publishDirectDeliveryFeedback(ctx context.Context) {
	var jobs []MatrixDMOutbound
	tx := db.GetDB().WithContext(ctx)
	if tx.Where("delivery_feedback_state IS NULL OR delivery_feedback_state = '' OR delivery_feedback_state IN ? OR ((delivery_feedback_at IS NULL OR delivery_feedback_at <= ?) AND ((delivery_feedback_state IN ('failed','skipped') AND COALESCE(delivery_notice_state,'') <> delivery_feedback_state) OR (delivery_feedback_state = 'delivered' AND delivery_notice_state IN ('failed','skipped'))))", []string{"queued", "sending"}, time.Now().Add(-time.Minute)).Order("CASE WHEN delivery_feedback_at IS NULL THEN 0 ELSE 1 END, delivery_feedback_at ASC, created_at ASC").Limit(20).Find(&jobs).Error != nil {
		return
	}
	for _, candidate := range jobs {
		if ctx.Err() != nil {
			return
		}
		userID := ""
		err := tx.Transaction(func(conn *gorm.DB) error {
			var job MatrixDMOutbound
			if err := conn.Clauses(clause.Locking{Strength: "UPDATE"}).First(&job, "event_id = ?", candidate.EventID).Error; err != nil {
				return err
			}
			var event MatrixEvent
			if err := conn.First(&event, "event_id = ? AND room_id = ?", job.EventID, job.RoomID).Error; err != nil {
				return err
			}
			// On revoked access do not write new account data or expose any details.
			if _, _, err := hs.outboundDirectAudience(conn, &job, &event); err != nil {
				if !errors.Is(err, errDirectMessageSkipped) && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				return conn.Model(&job).Updates(map[string]any{"delivery_feedback_state": "skipped", "delivery_feedback_at": time.Now()}).Error
			}
			status, err := directDeliveryAggregate(conn, &job)
			if err != nil {
				return err
			}
			if status.State == job.DeliveryFeedbackState {
				return conn.Model(&job).Update("delivery_feedback_at", time.Now()).Error
			}
			// Match retry/worker lock order: job, conversation, then account.
			var author models.Account
			if err := conn.Clauses(clause.Locking{Strength: "UPDATE"}).First(&author, job.SenderAccountID).Error; err != nil {
				return err
			}
			userID = hs.bridgeAccountID(author)
			if author.UserId == 0 {
				var mapping MatrixExternalActor
				if err := conn.Where("account_id = ?", author.Id).First(&mapping).Error; err != nil {
					return err
				}
				userID = mapping.MatrixID
			}
			const dataType = "org.community.direct_delivery"
			var data MatrixAccountData
			err = conn.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ? AND room_id = ? AND data_type = ?", userID, job.RoomID, dataType).First(&data).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// A bounded per-room summary; individual historical states remain available
			// from the author-only API rather than growing every /sync indefinitely.
			entries := []any{}
			if old, ok := data.Content["events"].([]any); ok {
				for _, item := range old {
					m, ok := item.(map[string]any)
					if ok && m["event_id"] != job.EventID {
						entries = append(entries, item)
					}
				}
			}
			if len(entries) > 31 {
				entries = entries[len(entries)-31:]
			}
			entries = append(entries, map[string]any{"event_id": status.EventID, "state": status.State, "remote_received": status.RemoteReceived, "error_code": status.ErrorCode, "can_retry": status.CanRetry, "updated_at": status.UpdatedAt})
			data.UserID = userID
			data.RoomID = &job.RoomID
			data.DataType = dataType
			data.Content = map[string]any{"events": entries}
			if err := conn.Save(&data).Error; err != nil {
				return err
			}
			return conn.Model(&job).Updates(map[string]any{"delivery_feedback_state": status.State, "delivery_feedback_at": time.Now()}).Error
		})
		if err != nil {
			// A missing/deleted event or transient database failure must not
			// let the first page starve unrelated authors' feedback forever.
			update := map[string]any{"delivery_feedback_at": time.Now()}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				update["delivery_feedback_state"] = "skipped"
				update["delivery_notice_state"] = "skipped"
			}
			_ = tx.Model(&MatrixDMOutbound{}).Where("event_id = ?", candidate.EventID).Updates(update).Error
		} else {
			if userID != "" {
				hs.NotifyUser(userID)
			}
			var latest MatrixDMOutbound
			if tx.First(&latest, "event_id = ?", candidate.EventID).Error == nil {
				noticeErr := hs.publishDirectDeliveryNotice(ctx, &latest)
				if errors.Is(noticeErr, errDirectMessageSkipped) {
					_ = tx.Model(&MatrixDMOutbound{}).Where("event_id = ? AND delivery_feedback_state = ?", latest.EventID, latest.DeliveryFeedbackState).Update("delivery_notice_state", latest.DeliveryFeedbackState).Error
				}
			}
		}
	}
}

func loadDirectMatrixDeliveryRows(tx *gorm.DB, ids []string) (map[string][]BridgeFederationDelivery, error) {
	result := map[string][]BridgeFederationDelivery{}
	if len(ids) == 0 {
		return result, nil
	}
	var rows []BridgeFederationDelivery
	if err := tx.Select("event_id", "state", "updated_at").Where("event_id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		result[row.EventID] = append(result[row.EventID], row)
	}
	return result, nil
}
func mergeDirectMatrixDelivery(state DirectDeliveryStatus, rows []BridgeFederationDelivery) DirectDeliveryStatus {
	rank := map[string]int{"delivered": 0, "skipped": 1, "queued": 2, "sending": 3, "failed": 4}
	remote := state.RemoteReceived
	for _, row := range rows {
		next := "queued"
		switch row.State {
		case "failed":
			next = "failed"
		case "processing":
			next = "sending"
		case "delivered":
			next = "delivered"
			remote = true
		case "skipped", "cancelled":
			next = "skipped"
		}
		if rank[next] > rank[state.State] {
			state.State = next
			if next == "failed" {
				state.ErrorCode = "matrix_delivery_failed"
				state.CanRetry = true
			}
		}
		if row.UpdatedAt.After(state.UpdatedAt) {
			state.UpdatedAt = row.UpdatedAt
		}
	}
	state.RemoteReceived = state.State == "delivered" && remote
	return state
}

// StartDirectDeliveryFeedback isolates slow remote notification invitations
// from message delivery. There is exactly one bounded worker per homeserver.
func (hs *HomeServer) StartDirectDeliveryFeedback(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				round, cancel := context.WithTimeout(ctx, 30*time.Second)
				hs.publishDirectDeliveryFeedback(round)
				cancel()
			}
		}
	}()
}
