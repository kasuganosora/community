package homeserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MatrixDMOutbound contains no plaintext. A job is admitted only by the local
// client send transaction, never by backfill, federation or the inbound bridge.
// Locking the row through status/outbox creation makes commit/retry atomic.
type MatrixDMOutbound struct {
	EventID               string     `gorm:"primaryKey;size:255"`
	RoomID                string     `gorm:"index;size:255;not null"`
	SourceStatusID        uint64     `gorm:"not null"`
	SenderAccountID       uint64     `gorm:"not null"`
	ConversationID        string     `gorm:"size:36;index"`
	RecipientIDs          []uint64   `gorm:"type:json;serializer:json"`
	MemberIDs             []uint64   `gorm:"type:json;serializer:json"`
	DeliveryNoticeState   string     `gorm:"size:16"`
	DeliveryFeedbackState string     `gorm:"size:16"`
	DeliveryFeedbackAt    *time.Time `gorm:"index"`
	StatusID              uint64     `gorm:"index"`
	State                 string     `gorm:"size:16;index;not null"`
	NextAttemptAt         time.Time  `gorm:"index;not null"`
	Attempts              int
	LastError             string `gorm:"size:96"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (MatrixDMOutbound) TableName() string { return "matrix_dm_outbound" }

// MatrixDMEventReference authenticates internal text/attachment event origins.
// It is written in the event transaction, never inferred from client content.
type MatrixDMEventReference struct {
	EventID  string `gorm:"primaryKey;size:255"`
	RoomID   string `gorm:"index;size:255;not null"`
	StatusID uint64 `gorm:"index;not null"`
}

func (MatrixDMEventReference) TableName() string { return "matrix_dm_event_references" }
func (e *EventService) sendBridgeRoomEvent(roomID, sender, eventType string, txnID *string, content map[string]any, statusID uint64) (*SendMessageResponse, *MatrixError) {
	return e.sendRoomEvent(roomID, sender, eventType, txnID, content, nil, false, statusID)
}

// Internal bridge events call SendRoomEvent, not the client-only enqueue path;
// a client-supplied marker or transaction ID can therefore never impersonate it.
func (hs *HomeServer) queueOutboundDirect(tx *gorm.DB, event *MatrixEvent) error {
	if handled, err := hs.queueDirectBridgeReaction(tx, event); handled || err != nil {
		return err
	}
	if !strings.HasPrefix(event.RoomID, "!apdm_") && !strings.HasPrefix(event.RoomID, "!apchat_") {
		return nil
	}
	normalized, _, err := hs.normalizeBridgeReplacement(tx, event)
	if err != nil {
		return err
	}
	event = normalized
	if handled, err := hs.queueConversationMessage(tx, event); handled || err != nil {
		return err
	}
	if event.EventType != EventTypeRoomMessage || event.StateKey != nil || !strings.HasPrefix(event.RoomID, "!apdm_") {
		return nil
	}
	relation, _ := event.Content["m.relates_to"].(map[string]any)
	if relation["rel_type"] == "m.replace" {
		return nil
	}
	var source models.MatrixDMDelivery
	err = tx.Joins("JOIN statuses ON statuses.id = matrix_dm_deliveries.status_id").Where("matrix_dm_deliveries.room_id = ? AND matrix_dm_deliveries.state = ? AND matrix_dm_deliveries.event_id <> '' AND statuses.deleted_at IS NULL AND statuses.visibility = ? AND statuses.scheduled_at IS NULL", event.RoomID, "delivered", models.StatusVisibilityDirect).Order("matrix_dm_deliveries.status_id DESC").First(&source).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// A freshly committed inbound event may already be visible while its
		// encompassing delivery is still processing further attachments.
		var reference MatrixDMEventReference
		err = tx.Joins("JOIN statuses ON statuses.id = matrix_dm_event_references.status_id").Where("matrix_dm_event_references.room_id = ? AND statuses.deleted_at IS NULL AND statuses.visibility = ? AND statuses.scheduled_at IS NULL", event.RoomID, models.StatusVisibilityDirect).Order("matrix_dm_event_references.status_id DESC").First(&reference).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errDirectMessageSkipped
		}
		source.StatusID = reference.StatusID
	}
	if err != nil {
		return err
	}
	var account models.Account
	localpart, server, ok := strings.Cut(strings.TrimPrefix(event.Sender, "@"), ":")
	if !ok || server != hs.Config.ServerName {
		return errDirectMessageSkipped
	}
	err = tx.Preload("User").Where("username = ? AND domain IS NULL", localpart).First(&account).Error
	if err != nil {
		return err
	}
	if account.UserId == 0 || account.User == nil || account.User.IsBan || account.User.Status != models.UserStatusNormal || account.IsBan {
		return errDirectMessageSkipped
	}
	job := MatrixDMOutbound{EventID: event.EventID, RoomID: event.RoomID, SourceStatusID: source.StatusID, SenderAccountID: account.Id, State: "pending", NextAttemptAt: time.Now()}
	origin, recipients, err := hs.outboundDirectAudience(tx, &job, event)
	if err != nil {
		return err
	}
	switch event.Content["msgtype"] {
	case "m.text", "m.notice", "m.emote":
	case "m.image", "m.video", "m.audio", "m.file":
		if err := hs.admitBridgeMedia(tx, event); err != nil {
			return err
		}
	default:
		return errDirectMessageSkipped
	}
	if _, _, err := hs.outboundReplyContent(tx, &job, event, origin, recipients, true); err != nil {
		return err
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error
}

// Exact participant hashing authenticates the original audience. A leave, ban,
// changed audience, redaction or block makes queued delivery fail closed; it
// never inherits a conversation's union of historical participants.
func (hs *HomeServer) outboundDirectAudience(tx *gorm.DB, job *MatrixDMOutbound, event *MatrixEvent) (*models.Status, []models.Account, error) {
	if job.ConversationID != "" {
		return hs.conversationOutboundAudience(tx, job, event)
	}
	var source models.Status
	if err := tx.Preload("ToAccounts").Preload("Account").Where("id = ? AND deleted_at IS NULL", job.SourceStatusID).First(&source).Error; err != nil {
		return nil, nil, err
	}
	if source.Visibility != models.StatusVisibilityDirect || source.ScheduledAt != nil || source.Account == nil {
		return nil, nil, errDirectMessageSkipped
	}
	accounts := map[uint64]models.Account{source.AccountId: *source.Account}
	for _, a := range source.ToAccounts {
		accounts[a.Id] = *a
	}
	sender, ok := accounts[job.SenderAccountID]
	if !ok || sender.Domain != nil || sender.IsBan || hs.bridgeAccountID(sender) != event.Sender {
		return nil, nil, errDirectMessageSkipped
	}
	var user models.User
	if err := tx.First(&user, sender.UserId).Error; err != nil {
		return nil, nil, err
	}
	if sender.UserId == 0 || user.IsBan || user.Status != models.UserStatusNormal {
		return nil, nil, errDirectMessageSkipped
	}
	ids := make([]string, 0, len(accounts))
	byID := map[string]bool{}
	recipients := make([]models.Account, 0, len(accounts))
	for _, a := range accounts {
		if a.IsBan {
			return nil, nil, errDirectMessageSkipped
		}
		blocked, err := models.AccountsBlocked(tx, &sender, &a)
		if err != nil {
			return nil, nil, err
		}
		if blocked {
			return nil, nil, errDirectMessageSkipped
		}
		if err := hs.validateBridgeAccountIdentity(tx, a); err != nil {
			return nil, nil, err
		}
		id := hs.bridgeAccountID(a)
		ids = append(ids, id)
		byID[id] = true
		if a.Id != sender.Id {
			recipients = append(recipients, a)
		}
	}
	if len(recipients) == 0 {
		return nil, nil, errDirectMessageSkipped
	}
	sort.Strings(ids)
	digest := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	hash := hex.EncodeToString(digest[:])
	if job.RoomID != "!apdm_"+hash+":"+hs.Config.ServerName {
		return nil, nil, errDirectMessageSkipped
	}
	creator := "@_ap_bridge_" + hash + ":" + hs.Config.ServerName
	var room MatrixRoom
	if err := tx.Where("room_id = ?", job.RoomID).First(&room).Error; err != nil {
		return nil, nil, err
	}
	if room.Creator != creator {
		return nil, nil, errDirectMessageSkipped
	}
	var encrypted int64
	if err := tx.Model(&MatrixRoomState{}).Where("room_id = ? AND event_type = ?", job.RoomID, EventTypeRoomEncryption).Count(&encrypted).Error; err != nil {
		return nil, nil, err
	}
	if encrypted != 0 {
		return nil, nil, errDirectMessageSkipped
	}
	var members []MatrixRoomMember
	if err := tx.Where("room_id = ? AND membership = ?", job.RoomID, MembershipJoin).Find(&members).Error; err != nil {
		return nil, nil, err
	}
	for _, m := range members {
		if m.UserID == creator {
			continue
		}
		if !byID[m.UserID] {
			return nil, nil, errDirectMessageSkipped
		}
		delete(byID, m.UserID)
	}
	if len(byID) != 0 {
		return nil, nil, errDirectMessageSkipped
	}
	return &source, recipients, nil
}

func (hs *HomeServer) processOutboundDirectJobs(ctx context.Context) {
	var jobs []MatrixDMOutbound
	if db.GetDB().WithContext(ctx).Where("state = ? AND next_attempt_at <= ?", "pending", time.Now()).Order("next_attempt_at,event_id").Limit(20).Find(&jobs).Error != nil {
		return
	}
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		_ = hs.ProcessOutboundDirect(ctx, job.EventID)
	}
}

func (hs *HomeServer) ProcessOutboundDirect(ctx context.Context, eventID string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ctx, publish := models.BeginDeferredStatusEvents(ctx)
	var notifyRoom string
	var remoteMedia *MatrixMedia
	var prefetchErr error
	var initialJob MatrixDMOutbound
	var initialEvent MatrixEvent
	readConn := db.GetDB().WithContext(ctx)
	if readConn.Where("event_id = ? AND state = ?", eventID, "pending").First(&initialJob).Error == nil && !initialJob.NextAttemptAt.After(time.Now()) && readConn.First(&initialEvent, "event_id = ?", eventID).Error == nil {
		if raw, ok := initialEvent.Content["url"].(string); ok && !strings.HasPrefix(raw, "mxc://"+hs.Config.ServerName+"/") {
			if _, _, prefetchErr = hs.outboundDirectAudience(readConn, &initialJob, &initialEvent); prefetchErr == nil {
				remoteMedia, prefetchErr = hs.importBridgeRemoteMedia(ctx, &initialEvent)
			}
		}
	}
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if prefetchErr != nil {
			return prefetchErr
		}
		// Match client PDU persistence: room -> conversation. Replacement
		// creates its notice in this transaction, so taking the room first
		// avoids a conv->room cycle with a concurrent client send.
		if initialJob.RoomID != "" {
			var room MatrixRoom
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&room, "room_id = ?", initialJob.RoomID).Error; err != nil {
				return err
			}
		}
		var job MatrixDMOutbound
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("event_id = ?", eventID).First(&job).Error; err != nil {
			return err
		}
		if job.State != "pending" || job.NextAttemptAt.After(time.Now()) {
			return nil
		}
		var event MatrixEvent
		if err := tx.Where("event_id = ? AND room_id = ?", job.EventID, job.RoomID).First(&event).Error; err != nil {
			return err
		}
		source, recipients, err := hs.outboundDirectAudience(tx, &job, &event)
		if err != nil {
			return err
		}
		if event.EventType != EventTypeRoomMessage {
			return errDirectMessageSkipped
		}
		originalEvent := event
		normalized, replaced, err := hs.normalizeBridgeReplacement(tx, &event)
		if err != nil {
			return err
		}
		event = *normalized
		if replaced != nil {
			allowed := map[uint64]bool{}
			for _, a := range recipients {
				allowed[a.Id] = true
			}
			recipients = nil
			for _, a := range replaced.ToAccounts {
				if !allowed[a.Id] {
					return errDirectMessageSkipped
				}
				recipients = append(recipients, *a)
			}
		}
		parent, body, err := hs.outboundReplyContent(tx, &job, &event, source, recipients, false)
		if err != nil {
			return err
		}
		ids := make([]uint64, 0, len(recipients))
		for _, a := range recipients {
			ids = append(ids, a.Id)
		}
		text, format, err := bridgeOutboundText(event.Content, parent != nil && parent.Id != 0)
		if err != nil {
			return err
		}
		if format != "html" {
			text = body
		}
		status := models.Status{AccountId: job.SenderAccountID, Visibility: models.StatusVisibilityDirect, Content: models.StatusContent{Content: text, ContentFormat: format}}
		if parent != nil && parent.Id != 0 {
			status.InReplyToId = &parent.Id
			status.InReplyToAccountId = &parent.AccountId
		}
		if replaced != nil {
			relation, _ := event.Content["m.relates_to"].(map[string]any)
			if relation["m.in_reply_to"] == nil {
				status.InReplyToId = replaced.InReplyToId
				status.InReplyToAccountId = replaced.InReplyToAccountId
			}
		}
		switch event.Content["msgtype"] {
		case "m.text", "m.notice", "m.emote":
		case "m.image", "m.video", "m.audio", "m.file":
			attachment, err := hs.outboundDirectAttachmentWithMedia(tx, &job, &event, remoteMedia)
			if err != nil {
				return err
			}
			status.Attachments = models.Attachments{attachment}
		default:
			return errDirectMessageSkipped
		}
		writeCtx := models.WithStructuredRecipientIDs(ctx, ids)
		if replaced != nil {
			writeCtx = models.WithDeferredFederationCreate(writeCtx)
		}
		scoped := tx.WithContext(writeCtx)
		if err := scoped.Create(&status).Error; err != nil {
			return err
		}
		if replaced != nil {
			if err := hs.finalizeBridgeReplacement(scoped, &originalEvent, replaced, &status); err != nil {
				return err
			}
			if err := models.QueueFederationReplacement(scoped, &status); err != nil {
				return err
			}
			notifyRoom = job.RoomID
		}
		// Map both directions atomically. This pre-completes the inbound job, so its
		// startup reconciliation can never echo this new Status back into Matrix.
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "status_id"}}, DoUpdates: clause.AssignmentColumns([]string{"state", "room_id", "event_id"})}).Create(&models.MatrixDMDelivery{StatusID: status.Id, State: "delivered", RoomID: job.RoomID, EventID: job.EventID, NextAttemptAt: time.Now()}).Error; err != nil {
			return err
		}
		return tx.Model(&job).Updates(map[string]any{"state": "delivered", "status_id": status.Id, "last_error": ""}).Error
	})
	if err == nil {
		publish()
		if notifyRoom != "" {
			hs.NotifyRoomEvent(notifyRoom)
		}
		return nil
	}
	state := "pending"
	if errors.Is(err, errDirectMessageSkipped) || errors.Is(err, gorm.ErrRecordNotFound) {
		state = "skipped"
	}
	attemptIncrement := 1
	next := time.Now().Add(time.Minute)
	if errors.Is(err, errOutboundParentPending) {
		next = time.Now().Add(2 * time.Second)
		attemptIncrement = 0
	}
	update := db.GetDB().WithContext(ctx).Model(&MatrixDMOutbound{}).Where("event_id = ? AND state = ?", eventID, "pending").Updates(map[string]any{"state": gorm.Expr("CASE WHEN ? = ? AND ? > 0 AND attempts >= 4 THEN ? ELSE ? END", state, "pending", attemptIncrement, "failed", state), "last_error": "direct_bridge_unavailable", "next_attempt_at": next, "attempts": gorm.Expr("attempts + ?", attemptIncrement)})
	if update.Error != nil {
		return update.Error
	}
	return err
}

func (hs *HomeServer) outboundDirectAttachment(tx *gorm.DB, job *MatrixDMOutbound, event *MatrixEvent) (*models.MediaAttachment, error) {
	return hs.outboundDirectAttachmentWithMedia(tx, job, event, nil)
}
func (hs *HomeServer) outboundDirectAttachmentWithMedia(tx *gorm.DB, job *MatrixDMOutbound, event *MatrixEvent, media *MatrixMedia) (*models.MediaAttachment, error) {
	var err error
	if media == nil {
		media, err = hs.outboundMediaRecord(tx, event)
		if err != nil {
			return nil, err
		}
	}
	if media.RemoteURL != "" {
		return &models.MediaAttachment{AccountId: job.SenderAccountID, RemoteUrl: media.RemoteURL, FileName: media.FileName, Description: safeBridgeFilename(media.FileName), FileSize: media.FileSize, ContentType: media.ContentType, Type: models.GetMediaType(media.ContentType), Width: media.Width, Height: media.Height}, nil
	}
	var reader io.ReadCloser
	if media.transientData != nil {
		if int64(len(media.transientData)) != media.FileSize {
			return nil, fmt.Errorf("bridge attachment size mismatch")
		}
		reader = io.NopCloser(bytes.NewReader(media.transientData))
	} else {
		var info storage.ObjectInfo
		reader, info, err = storage.Open(tx.Statement.Context, media.FilePath)
		if err != nil {
			return nil, err
		}
		if info.Size != media.FileSize {
			reader.Close()
			return nil, fmt.Errorf("bridge attachment size mismatch")
		}
	}
	defer reader.Close()
	hash := sha256.Sum256([]byte(job.EventID))
	origin := media.Origin
	if origin != "remote" {
		origin = "local"
	}
	key := "media/" + origin + "/matrix-direct/" + hex.EncodeToString(hash[:]) + path.Ext(media.FileName)
	if err := storage.Default().Put(tx.Statement.Context, key, io.LimitReader(reader, media.FileSize), media.FileSize, media.ContentType); err != nil {
		return nil, err
	}
	return &models.MediaAttachment{AccountId: job.SenderAccountID, FileName: path.Base(key), Description: safeBridgeFilename(media.FileName), FilePath: strings.TrimPrefix(key, models.MediaKeyPrefix), FileSize: media.FileSize, ContentType: media.ContentType, Type: models.GetMediaType(media.ContentType), Width: media.Width, Height: media.Height}, nil
}

func (hs *HomeServer) outboundReplyContent(tx *gorm.DB, job *MatrixDMOutbound, event *MatrixEvent, source *models.Status, recipients []models.Account, admission bool) (*models.Status, string, error) {
	relation, _ := event.Content["m.relates_to"].(map[string]any)
	body, _ := event.Content["body"].(string)
	parent := source
	if reply, ok := relation["m.in_reply_to"].(map[string]any); ok {
		target, _ := reply["event_id"].(string)
		if target == "" {
			return nil, "", errDirectMessageSkipped
		}
		var mapping models.MatrixDMDelivery
		if err := resolveDirectEventStatus(tx, job.RoomID, target, &mapping); err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, "", err
			}
			// A just-sent reply can precede its parent's AP job. It is already
			// authenticated by that parent's client transaction; do not reject it
			// merely because the asynchronous worker has not committed its mapping.
			var pending MatrixDMOutbound
			if err := tx.Where("room_id = ? AND event_id = ?", job.RoomID, target).First(&pending).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil, "", errDirectMessageSkipped
				}
				return nil, "", err
			}
			if pending.State != "pending" {
				return nil, "", errDirectMessageSkipped
			}
			if !admission {
				return nil, "", errOutboundParentPending
			}
			if strings.TrimSpace(body) == "" || len(body) > 65536 {
				return nil, "", errDirectMessageSkipped
			}
			return source, body, nil
		}
		var replied models.Status
		if err := tx.Preload("ToAccounts").Where("id = ? AND deleted_at IS NULL AND visibility = ?", mapping.StatusID, models.StatusVisibilityDirect).First(&replied).Error; err != nil {
			return nil, "", err
		}
		for _, a := range append(recipients, models.Account{Id: job.SenderAccountID}) {
			allowed, err := replied.CanViewEx(tx, &a)
			if err != nil {
				return nil, "", err
			}
			if !allowed {
				return nil, "", errDirectMessageSkipped
			}
		}
		parent = &replied
		// Matrix plain-text fallback is removed only after authenticating the reply.
		if strings.HasPrefix(body, "> ") {
			if _, tail, ok := strings.Cut(body, "\n\n"); ok {
				body = tail
			}
		}
	}
	if strings.TrimSpace(body) == "" || len(body) > 65536 {
		return nil, "", errDirectMessageSkipped
	}

	return parent, body, nil
}

func (hs *HomeServer) outboundMediaRecord(tx *gorm.DB, event *MatrixEvent) (*MatrixMedia, error) {
	raw, _ := event.Content["url"].(string)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "mxc" || u.Host != hs.Config.ServerName || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(strings.TrimPrefix(u.Path, "/"), "/") {
		return nil, errDirectMessageSkipped
	}
	if _, encrypted := event.Content["file"]; encrypted {
		return nil, errDirectMessageSkipped
	}
	var media MatrixMedia
	if err := tx.Where("server_name = ? AND media_id = ? AND uploader_id = ?", u.Host, strings.TrimPrefix(u.Path, "/"), event.Sender).First(&media).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errDirectMessageSkipped
		}
		return nil, err
	}
	if media.FileSize <= 0 || media.FileSize > 64<<20 {
		return nil, errDirectMessageSkipped
	}

	return &media, nil
}

var errOutboundParentPending = errors.New("direct reply parent pending")

const directBridgeTopic = "This conversation is not end-to-end encrypted. New messages, replies and attachments are delivered to its explicitly addressed ActivityPub and Matrix participants. Editing your message withdraws the previous ActivityPub post and sends a new one; deleting your own message propagates its withdrawal. Remote delivery does not mean it has been read."

// Update the old, Matrix-only notice without scanning or exporting any past
// messages. Only a recorded bridge room with its deterministic owner qualifies.
func (hs *HomeServer) refreshDirectBridgeTopics(ctx context.Context) {
	var after string
	for ctx.Err() == nil {
		var rooms []MatrixRoom
		conn := db.GetDB().WithContext(ctx)
		if err := conn.Select("room_id", "creator").Where("room_id LIKE ? AND room_id > ?", "!apdm_%", after).Order("room_id").Limit(50).Find(&rooms).Error; err != nil {
			return
		}
		if len(rooms) == 0 {
			return
		}
		for _, room := range rooms {
			after = room.RoomID
			suffix := ":" + hs.Config.ServerName
			hash := strings.TrimSuffix(strings.TrimPrefix(room.RoomID, "!apdm_"), suffix)
			if len(hash) != 64 || room.Creator != "@_ap_bridge_"+hash+suffix {
				continue
			}
			var count int64
			if conn.Model(&models.MatrixDMDelivery{}).Where("room_id = ? AND state = ?", room.RoomID, "delivered").Limit(1).Count(&count).Error != nil {
				return
			}
			if count == 0 {
				continue
			}
			_ = hs.syncBridgeRoomName(ctx, room.RoomID)
			var current MatrixRoomState
			if err := conn.Where("room_id = ? AND event_type = ? AND state_key = ?", room.RoomID, "m.room.topic", "").First(&current).Error; err == nil && current.Content["topic"] == directBridgeTopic {
				continue
			}
			key := ""
			if _, err := hs.Event.SendRoomEvent(room.RoomID, room.Creator, "m.room.topic", nil, map[string]any{"topic": directBridgeTopic}, &key); err != nil {
				return
			}
		}
	}
}

func resolveDirectEventStatus(tx *gorm.DB, roomID, eventID string, mapping *models.MatrixDMDelivery) error {
	err := tx.Where("room_id = ? AND event_id = ? AND state = ?", roomID, eventID, "delivered").First(mapping).Error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	var reference MatrixDMEventReference
	if err := tx.Where("room_id = ? AND event_id = ?", roomID, eventID).First(&reference).Error; err != nil {
		return err
	}
	mapping.StatusID = reference.StatusID
	return nil
}

func (hs *HomeServer) admitBridgeMedia(tx *gorm.DB, event *MatrixEvent) error {
	raw, _ := event.Content["url"].(string)
	if strings.HasPrefix(raw, "mxc://"+hs.Config.ServerName+"/") {
		_, err := hs.outboundMediaRecord(tx, event)
		return err
	}
	_, _, err := hs.validateBridgeRemoteMXC(event)
	return err
}
