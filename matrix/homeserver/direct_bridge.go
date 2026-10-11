package homeserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/activitypub"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StartDirectMessageBridge runs a separate durable queue. AP is plaintext;
// these private conversations never pretend to provide E2EE.
func (hs *HomeServer) StartDirectMessageBridge(ctx context.Context) {
	go func() {
		hs.refreshDirectBridgeTopics(ctx)
		hs.refreshExplicitBridgeRoomNames(ctx)
	}()
	hs.StartDirectDeliveryFeedback(ctx)
	hs.StartBridgeConversationReconciler(ctx)
	hs.StartBridgeFederation(ctx)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var cursor uint64
		backfillDone := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				hs.processOutboundDirectJobs(ctx)
				hs.processDirectBridgeEventJobs(ctx)
				conn := db.GetDB().WithContext(ctx)
				// Bounded startup reconciliation also covers messages received before this feature.
				var statuses []models.Status
				if !backfillDone && conn.Select("id", "visibility").Where("id > ? AND visibility = ? AND deleted_at IS NULL AND scheduled_at IS NULL AND reblog_id IS NULL", cursor, models.StatusVisibilityDirect).Order("id").Limit(100).Find(&statuses).Error == nil {
					if len(statuses) == 0 {
						backfillDone = true
					}
					for _, s := range statuses {
						if ctx.Err() != nil {
							return
						}
						if err := models.QueueMatrixDirectMessage(conn, &s); err != nil {
							break
						}
						cursor = s.Id
					}
				}
				var jobs []models.MatrixDMDelivery
				if conn.Where("state IN ? AND next_attempt_at <= ?", []string{"pending", "processing"}, time.Now()).Order("status_id").Limit(25).Find(&jobs).Error != nil {
					continue
				}
				for _, job := range jobs {
					if ctx.Err() != nil {
						return
					}
					_ = hs.ProcessDirectMessage(ctx, job.StatusID)
				}
			}
		}
	}()
}

// ProcessDirectMessage is restart/retry safe through a fenced lease, a stable
// private room ID and stable Matrix transaction IDs for text and attachments.
func (hs *HomeServer) ProcessDirectMessage(ctx context.Context, statusID uint64) error {
	conn := db.GetDB().WithContext(ctx)
	now := time.Now()
	lease := uuid.NewString()
	claim := conn.Model(&models.MatrixDMDelivery{}).Where("status_id = ? AND state IN ? AND next_attempt_at <= ?", statusID, []string{"pending", "processing"}, now).Updates(map[string]any{"state": "processing", "lease_token": lease, "next_attempt_at": now.Add(5 * time.Minute)})
	if claim.Error != nil || claim.RowsAffected == 0 {
		return claim.Error
	}
	workCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	room, event, err := hs.bridgeDirectMessage(workCtx, statusID)
	state := "delivered"
	message := ""
	next := now
	if err != nil {
		state = "pending"
		message = "private message bridge retry required"
		next = now.Add(time.Minute)
	}
	if errors.Is(err, errDirectMessageSkipped) {
		state = "skipped"
		message = ""
		err = nil
	}
	update := conn.Model(&models.MatrixDMDelivery{}).Where("status_id = ? AND lease_token = ?", statusID, lease).Updates(map[string]any{"state": state, "room_id": room, "event_id": event, "last_error": message, "lease_token": "", "next_attempt_at": next, "attempts": gorm.Expr("attempts + 1")})
	if update.Error != nil {
		return update.Error
	}
	if state == "delivered" && update.RowsAffected > 0 {
		_ = hs.syncBridgeRoomName(workCtx, room)
	}
	return err
}

var errDirectMessageSkipped = errors.New("direct message unavailable")

func (hs *HomeServer) bridgeAccountID(a models.Account) string {
	if a.Domain == nil {
		return fmt.Sprintf("@%s:%s", a.Username, hs.Config.ServerName)
	}
	// Longer than the allowed local username: cannot collide with a login account.
	sum := sha256.Sum256([]byte(fmt.Sprintf("activitypub-account:%d", a.Id)))
	return "@_ap_" + hex.EncodeToString(sum[:]) + ":" + hs.Config.ServerName
}
func (hs *HomeServer) bridgeDirectMessage(ctx context.Context, statusID uint64) (string, string, error) {
	conn := db.GetDB().WithContext(ctx)
	var status models.Status
	if err := conn.Preload("Content").Where("id = ? AND deleted_at IS NULL", statusID).First(&status).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", "", errDirectMessageSkipped
		}
		return "", "", err
	}
	if status.Visibility != models.StatusVisibilityDirect || status.ScheduledAt != nil || status.ReblogId != nil {
		return "", "", errDirectMessageSkipped
	}
	// Never echo messages created by the external Matrix application-service bridge.
	var mapped int64
	if conn.Migrator().HasTable(&models.MatrixMessage{}) {
		if err := conn.Model(&models.MatrixMessage{}).Where("status_id = ? AND direction = ?", statusID, "from_matrix").Count(&mapped).Error; err != nil {
			return "", "", err
		}
		if mapped > 0 {
			return "", "", errDirectMessageSkipped
		}
	}
	var sender models.Account
	if err := conn.First(&sender, status.AccountId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", "", errDirectMessageSkipped
		}
		return "", "", err
	}
	if sender.IsBan {
		return "", "", errDirectMessageSkipped
	}
	var targets []models.Account
	if err := conn.Joins("JOIN status_to_accounts ON status_to_accounts.account_id = accounts.id").Where("status_to_accounts.status_id = ?", statusID).Find(&targets).Error; err != nil {
		return "", "", err
	}
	participants := map[string]models.Account{hs.bridgeAccountID(sender): sender}
	localRecipients := 0
	for _, a := range targets {
		if a.IsBan {
			continue
		}
		blocked, err := models.AccountsBlocked(conn, &sender, &a)
		if err != nil {
			return "", "", err
		}
		if blocked {
			continue
		}
		participants[hs.bridgeAccountID(a)] = a
		if a.Domain == nil && a.Id != sender.Id {
			localRecipients++
		}
	}
	if localRecipients == 0 {
		return "", "", errDirectMessageSkipped
	}
	roomID, creator, remapped, explicit, routeErr := hs.explicitBridgeRoomForStatus(ctx, &status, participants)
	if routeErr != nil {
		return roomID, "", routeErr
	}
	if explicit {
		participants = remapped
	}
	ids := make([]string, 0, len(participants))
	for id := range participants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	digest := sha256.Sum256([]byte(strings.Join(ids, "\x00")))
	if !explicit {
		roomID = "!apdm_" + hex.EncodeToString(digest[:]) + ":" + hs.Config.ServerName
		creator = "@_ap_bridge_" + hex.EncodeToString(digest[:]) + ":" + hs.Config.ServerName
	}
	var room MatrixRoom
	err := conn.Where("room_id = ?", roomID).First(&room).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if explicit {
			return roomID, "", errors.New("conversation room unavailable")
		}
		name := "Fediverse private messages"
		topic := directBridgeTopic
		_, me := hs.Room.CreateRoom(creator, &CreateRoomRequest{bridgeRoomID: roomID, Visibility: "private", Preset: "private_chat", IsDirect: len(ids) == 2, Name: &name, Topic: &topic, Invite: ids, CreationContent: map[string]any{"m.federate": false}, PowerLevelContentOverride: &PowerLevelContent{Users: map[string]int64{creator: 100}, EventsDefault: 0, StateDefault: 100, Invite: 100, Kick: 100, Ban: 100, Redact: 100}})
		if me != nil {
			return roomID, "", errors.New(me.Error)
		}
	} else if err != nil {
		return roomID, "", err
	} else if room.Creator != creator {
		return roomID, "", errors.New("bridge room owner mismatch")
	}
	// Only recipients invited by this bridge are joined. A deliberate leave/ban
	// is respected; future messages must not silently force them back in.
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return roomID, "", err
		}
		member, membershipErr := hs.Room.GetUserMembership(roomID, id)
		if membershipErr != nil {
			return roomID, "", membershipErr
		}
		_, memberServer, _ := ParseMatrixUserID(id)
		if member == MembershipInvite && memberServer == hs.Config.ServerName {
			if _, me := hs.Room.JoinRoom(id, roomID, &JoinRoomRequest{}); me != nil {
				return roomID, "", errors.New(me.Error)
			}
		}
	}
	// Reject altered/encrypted rooms and any unexpected joined members.
	var encryption int64
	if err = conn.Model(&MatrixRoomState{}).Where("room_id = ? AND event_type = ?", roomID, EventTypeRoomEncryption).Count(&encryption).Error; err != nil {
		return roomID, "", err
	}
	if encryption > 0 {
		return roomID, "", errors.New("AP cannot populate an encrypted room")
	}
	var members []MatrixRoomMember
	if err = conn.Where("room_id = ? AND membership = ?", roomID, MembershipJoin).Find(&members).Error; err != nil {
		return roomID, "", err
	}
	for _, m := range members {
		if m.UserID != creator {
			if _, ok := participants[m.UserID]; !ok {
				return roomID, "", errors.New("unexpected private room member")
			}
		}
	}
	senderID := hs.bridgeAccountID(sender)
	for mxid, account := range participants {
		if account.Id == sender.Id {
			senderID = mxid
			break
		}
	}
	avatarBudget, cancelAvatars := context.WithTimeout(ctx, 3*time.Second)
	defer cancelAvatars()
	// Refresh existing memberships on each new message as well as first use.
	// The room identity stays stable; recipients need not recreate old rooms.
	for _, member := range members {
		_, memberServer, _ := ParseMatrixUserID(member.UserID)
		if memberServer != hs.Config.ServerName {
			continue
		}
		account, ok := participants[member.UserID]
		if !ok {
			continue
		}
		if err := hs.syncBridgeProfile(ctx, avatarBudget, roomID, member.UserID, account); err != nil {
			return roomID, "", err
		}
	}

	txn := fmt.Sprintf("ap-status-%d", statusID)
	actorName := sender.Username
	if sender.Domain != nil {
		actorName += "@" + *sender.Domain
	}
	content := bridgeStatusContent(&status, actorName)
	if err := bridgeReplyRelation(conn, roomID, &status, content); err != nil {
		return roomID, "", err
	}
	resp, me := hs.Event.sendBridgeRoomEvent(roomID, senderID, "m.room.message", &txn, content, statusID)
	if me != nil {
		return roomID, "", errors.New(me.Error)
	}
	var attachments []models.MediaAttachment
	if conn.Migrator().HasTable(&models.MediaAttachment{}) {
		if err = conn.Where("status_id = ? AND account_id = ?", statusID, sender.Id).Find(&attachments).Error; err != nil {
			return roomID, resp.EventID, err
		}
	}
	for _, a := range attachments {
		if err := ctx.Err(); err != nil {
			return roomID, resp.EventID, err
		}
		if err = hs.bridgeDirectAttachment(ctx, roomID, senderID, statusID, a, status.Sensitive || status.Content.SpoilerText != ""); err != nil {
			return roomID, resp.EventID, err
		}
	}
	return roomID, resp.EventID, nil
}

func (hs *HomeServer) bridgeDirectAttachment(ctx context.Context, roomID, sender string, statusID uint64, a models.MediaAttachment, sensitive bool) error {
	txn := fmt.Sprintf("ap-status-%d-media-%d", statusID, a.Id)
	hash := sha256.Sum256([]byte(roomID + ":" + sender + ":" + txn))
	var existing int64
	if err := db.GetDB().Model(&MatrixEvent{}).Where("txn_key = ?", "$"+hex.EncodeToString(hash[:])).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	if a.FilePath == "" {
		if a.RemoteUrl == "" {
			return errors.New("attachment bytes unavailable")
		}
		data, contentType, filename, err := fetchDirectAttachmentNamed(ctx, a.RemoteUrl)
		if err != nil {
			return err
		}
		if a.Description == "" && filename != "" {
			a.Description = filename
		}
		media, err := NewMediaService(hs).UploadMedia(bytes.NewReader(data), &multipart.FileHeader{Filename: bridgeAttachmentFilename(a)}, contentType, sender)
		if err != nil {
			return err
		}
		return hs.sendDirectAttachment(roomID, sender, txn, statusID, a, *media, sensitive)
	}
	key := a.StorageKey()
	reader, _, err := storage.Open(ctx, key)
	if err != nil {
		return err
	}
	_ = reader.Close()
	conn := db.GetDB().WithContext(ctx)
	var media MatrixMedia
	err = conn.Where("server_name = ? AND uploader_id = ? AND file_path = ?", hs.Config.ServerName, sender, key).First(&media).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		media = MatrixMedia{MediaID: generateMediaID(), ServerName: hs.Config.ServerName, UploaderID: sender, FilePath: key, FileName: bridgeAttachmentFilename(a), ContentType: a.ContentType, FileSize: a.FileSize, Width: a.Width, Height: a.Height}
		err = conn.Clauses(clause.OnConflict{DoNothing: true}).Create(&media).Error
	}
	if err != nil {
		return err
	}
	return hs.sendDirectAttachment(roomID, sender, txn, statusID, a, media, sensitive)
}
func (hs *HomeServer) sendDirectAttachment(roomID, sender, txn string, statusID uint64, a models.MediaAttachment, media MatrixMedia, sensitive ...bool) error {
	_, me := hs.Event.sendBridgeRoomEvent(roomID, sender, "m.room.message", &txn, bridgeAttachmentContent(hs.Config.ServerName, a, media, len(sensitive) > 0 && sensitive[0]), statusID)
	if me != nil {
		return errors.New(me.Error)
	}
	return nil
}

// Use the same DNS-pinned transport as authenticated federation media, but a
// generic attachment Accept header and a bounded 64 MiB body (not emoji limits).
func fetchDirectAttachment(ctx context.Context, raw string) ([]byte, string, error) {
	data, contentType, _, err := fetchDirectAttachmentNamed(ctx, raw)
	return data, contentType, err
}

func bridgeDownloadFilename(disposition string) string {
	_, params, err := mime.ParseMediaType(disposition)
	if err != nil || params["filename"] == "" {
		return ""
	}
	return safeBridgeFilename(params["filename"])
}

func fetchDirectAttachmentNamed(ctx context.Context, raw string) ([]byte, string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return nil, "", "", errors.New("invalid private attachment URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", "", err
	}
	req.Header.Set("Accept", "*/*")
	client := activitypub.NewAuthenticatedMediaHTTPClient(nil)
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	const max = 64 << 20
	if resp.StatusCode != http.StatusOK || resp.ContentLength > max {
		return nil, "", "", errors.New("attachment unavailable or too large")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, "", "", err
	}
	if len(data) > max {
		return nil, "", "", errors.New("attachment too large")
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return data, contentType, bridgeDownloadFilename(resp.Header.Get("Content-Disposition")), nil
}
