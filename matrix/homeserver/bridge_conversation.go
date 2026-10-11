package homeserver

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Membership generations are append-only. A removed and re-added identity gets
// a new row; a queued message cannot silently grant its old audience to it.
type BridgeConversation struct {
	ID               string    `gorm:"primaryKey;size:36" json:"id"`
	RoomID           string    `gorm:"uniqueIndex;size:255;not null" json:"room_id"`
	CreatorAccountID uint64    `gorm:"not null" json:"-"`
	Controller       string    `gorm:"size:255;not null" json:"-"`
	RequestKey       string    `gorm:"uniqueIndex;size:64;not null" json:"-"`
	State            string    `gorm:"size:16;not null" json:"-"`
	Revision         uint64    `gorm:"not null" json:"revision"`
	Legacy           bool      `json:"-"`
	CreatedAt        time.Time `json:"-"`
}

func (BridgeConversation) TableName() string { return "matrix_bridge_conversations" }

type BridgeConversationMember struct {
	ID             uint64 `gorm:"primaryKey" json:"id,string"`
	ConversationID string `gorm:"index;size:36;not null" json:"-"`
	MatrixID       string `gorm:"index;size:255;not null" json:"matrix_id"`
	AccountID      uint64 `gorm:"index" json:"account_id,string"`
	Kind           string `gorm:"size:16;not null" json:"kind"`
	JoinedRevision uint64 `json:"-"`
	LeftRevision   uint64 `gorm:"index" json:"-"`
	JoinedAt       int64  `json:"-"`
}

func (BridgeConversationMember) TableName() string { return "matrix_bridge_conversation_members" }

type BridgeMemberInput struct {
	AccountID string `json:"account_id"`
	MatrixID  string `json:"matrix_id"`
}
type BridgeConversationRequest struct {
	TransactionID    string              `json:"transaction_id"`
	PlaintextConsent bool                `json:"plaintext_consent"`
	Members          []BridgeMemberInput `json:"members"`
}

func (hs *HomeServer) bridgeLocalAccount(tx *gorm.DB, mxid string) (*models.Account, error) {
	local, domain, ok := strings.Cut(strings.TrimPrefix(mxid, "@"), ":")
	if !ok || !strings.HasPrefix(mxid, "@") || domain != hs.Config.ServerName {
		return nil, errDirectMessageSkipped
	}
	var a models.Account
	if err := tx.Preload("User").Where("username = ? AND domain IS NULL", local).First(&a).Error; err != nil {
		return nil, err
	}
	if a.UserId == 0 || a.User == nil || a.User.IsBan || a.User.Status != models.UserStatusNormal || a.IsBan {
		return nil, errDirectMessageSkipped
	}
	return &a, nil
}
func (hs *HomeServer) newBridgeMember(tx *gorm.DB, conv *BridgeConversation, input BridgeMemberInput, owner *models.Account) (BridgeConversationMember, error) {
	m := BridgeConversationMember{ConversationID: conv.ID, JoinedRevision: conv.Revision, JoinedAt: time.Now().UnixMilli()}
	if input.AccountID != "" {
		id, err := strconv.ParseUint(input.AccountID, 10, 64)
		if err != nil {
			return m, errDirectMessageSkipped
		}
		var a models.Account
		if err := tx.First(&a, id).Error; err != nil {
			return m, err
		}
		blocked, err := models.AccountsBlocked(tx, owner, &a)
		if err != nil {
			return m, err
		}
		if blocked || a.IsBan {
			return m, errDirectMessageSkipped
		}
		m.MatrixID = hs.bridgeAccountID(a)
		m.AccountID = a.Id
		m.Kind = "ap"
		if a.Domain == nil {
			if _, err := hs.bridgeLocalAccount(tx, m.MatrixID); err != nil {
				return m, err
			}
			m.Kind = "local"
		}
	} else {
		if !strings.HasPrefix(input.MatrixID, "@") || len(input.MatrixID) > 255 || strings.ContainsAny(input.MatrixID, " \r\n\t/") {
			return m, errDirectMessageSkipped
		}
		_, domain, ok := strings.Cut(input.MatrixID[1:], ":")
		if !ok || domain == "" {
			return m, errDirectMessageSkipped
		}
		if domain == hs.Config.ServerName {
			a, err := hs.bridgeLocalAccount(tx, input.MatrixID)
			if err != nil {
				return m, err
			}
			m.AccountID = a.Id
			m.Kind = "local"
		} else {
			m.Kind = "matrix"
		}
		m.MatrixID = input.MatrixID
	}
	return m, nil
}
func (hs *HomeServer) CreateBridgeConversation(ctx context.Context, userID string, req BridgeConversationRequest) (*BridgeConversation, error) {
	if !req.PlaintextConsent || len(req.TransactionID) < 1 || len(req.TransactionID) > 128 || len(req.Members) < 1 || len(req.Members) > 31 {
		return nil, errDirectMessageSkipped
	}
	conn := db.GetDB().WithContext(ctx)
	owner, err := hs.bridgeLocalAccount(conn, userID)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256([]byte(userID + "\x00" + req.TransactionID))
	requestKey := hex.EncodeToString(hash[:])
	conv := BridgeConversation{}
	err = conn.Transaction(func(tx *gorm.DB) error {
		e := tx.Where("request_key = ?", requestKey).First(&conv).Error
		if e == nil {
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		id := uuid.NewString()
		conv = BridgeConversation{ID: id, RoomID: "!apchat_" + id + ":" + hs.Config.ServerName, CreatorAccountID: owner.Id, Controller: "@_ap_bridge_chat_" + requestKey + ":" + hs.Config.ServerName, RequestKey: requestKey, State: "provisioning", Revision: 1}
		if err := tx.Create(&conv).Error; err != nil {
			return err
		}
		all := append([]BridgeMemberInput{{AccountID: strconv.FormatUint(owner.Id, 10)}}, req.Members...)
		seen := map[string]bool{}
		for _, input := range all {
			m, err := hs.newBridgeMember(tx, &conv, input, owner)
			if err != nil {
				return err
			}
			if seen[m.MatrixID] {
				continue
			}
			seen[m.MatrixID] = true
			if err := tx.Create(&m).Error; err != nil {
				return err
			}
		}
		if len(seen) < 2 {
			return errDirectMessageSkipped
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = hs.provisionBridgeConversation(ctx, &conv); err != nil {
		return nil, err
	}
	return &conv, nil
}

var bridgeProvisionSlot = make(chan struct{}, 1)

// Dedicated session locks serialize reconcilers without introducing a room /
// conversation row-lock inversion. Membership mutations remain short SQL
// transactions; their revision CAS prevents an old reconciliation going live.
func lockBridgeProvision(ctx context.Context, conn *gorm.DB, id string) (func(), error) {
	select {
	case bridgeProvisionSlot <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release, err := lockBridgeProvisionSession(ctx, conn, id)
	if err != nil {
		<-bridgeProvisionSlot
		return nil, err
	}
	return func() { release(); <-bridgeProvisionSlot }, nil
}
func lockBridgeProvisionSession(ctx context.Context, conn *gorm.DB, id string) (func(), error) {
	if conn.Dialector.Name() == "sqlite" {
		return func() {}, nil
	}
	raw, err := conn.DB()
	if err != nil {
		return nil, err
	}
	if raw.Stats().MaxOpenConnections == 1 {
		return nil, errors.New("mixed conversations require at least two SQL connections")
	}
	leaseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	session, err := raw.Conn(leaseCtx)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte("community:bridge-provision:" + id))
	mysql := conn.Dialector.Name() == "mysql"
	var release func(context.Context) error
	if mysql {
		key := hex.EncodeToString(digest[:])
		var locked int
		if err = session.QueryRowContext(leaseCtx, "SELECT GET_LOCK(?, 3)", key).Scan(&locked); err != nil || locked != 1 {
			if err != nil {
				_ = session.Raw(func(any) error { return driver.ErrBadConn })
			}
			session.Close()
			if err == nil {
				err = errors.New("conversation reconciliation busy")
			}
			return nil, err
		}
		release = func(ctx context.Context) error {
			var released int
			err := session.QueryRowContext(ctx, "SELECT RELEASE_LOCK(?)", key).Scan(&released)
			if err == nil && released != 1 {
				return errors.New("reconciliation unlock failed")
			}
			return err
		}
	} else if conn.Dialector.Name() == "postgres" {
		key := int64(binary.BigEndian.Uint64(digest[:8]))
		var locked bool
		if err = session.QueryRowContext(leaseCtx, "SELECT pg_try_advisory_lock($1)", key).Scan(&locked); err != nil || !locked {
			if err != nil {
				_ = session.Raw(func(any) error { return driver.ErrBadConn })
			}
			session.Close()
			if err == nil {
				err = errors.New("conversation reconciliation busy")
			}
			return nil, err
		}
		release = func(ctx context.Context) error {
			var released bool
			err := session.QueryRowContext(ctx, "SELECT pg_advisory_unlock($1)", key).Scan(&released)
			if err == nil && !released {
				return errors.New("reconciliation unlock failed")
			}
			return err
		}
	} else {
		session.Close()
		return nil, errors.New("unsupported conversation lock dialect")
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := release(unlockCtx); err != nil {
			_ = session.Raw(func(any) error { return driver.ErrBadConn })
		}
		_ = session.Close()
	}, nil
}
func (hs *HomeServer) provisionBridgeConversation(ctx context.Context, conv *BridgeConversation) error {
	conn := db.GetDB().WithContext(ctx)
	release, err := lockBridgeProvision(ctx, conn, conv.ID)
	if err != nil {
		return err
	}
	defer release()
	var current BridgeConversation
	if err := conn.First(&current, "id = ?", conv.ID).Error; err != nil {
		return err
	}
	if current.State == "active" {
		*conv = current
		return nil
	}
	if err := hs.applyBridgeConversationMembers(ctx, &current); err != nil {
		return err
	}
	result := conn.Model(&BridgeConversation{}).Where("id = ? AND revision = ? AND state = ?", current.ID, current.Revision, "provisioning").Update("state", "active")
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("conversation membership changed during provisioning")
	}
	current.State = "active"
	*conv = current
	return nil
}
func (hs *HomeServer) applyBridgeConversationMembers(ctx context.Context, conv *BridgeConversation) error {
	conn := db.GetDB().WithContext(ctx)
	var room MatrixRoom
	err := conn.Where("room_id = ?", conv.RoomID).First(&room).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		name := "Fediverse conversation"
		topic := directBridgeTopic
		_, me := hs.Room.CreateRoom(conv.Controller, &CreateRoomRequest{bridgeRoomID: conv.RoomID, Visibility: "private", Preset: "private_chat", Name: &name, Topic: &topic, CreationContent: map[string]any{"m.federate": true}, InitialState: []CreateRoomStateEvent{{Type: "m.room.history_visibility", StateKey: "", Content: map[string]any{"history_visibility": "joined"}}}, PowerLevelContentOverride: &PowerLevelContent{Users: map[string]int64{conv.Controller: 100}, StateDefault: 100, Invite: 100, Kick: 100, Ban: 100, Redact: 100}})
		if me != nil {
			return errors.New(me.Error)
		}
	} else if err != nil {
		return err
	} else if room.Creator != conv.Controller {
		return errDirectMessageSkipped
	}
	var members []BridgeConversationMember
	if err := conn.Where("conversation_id = ? AND left_revision = 0", conv.ID).Find(&members).Error; err != nil {
		return err
	}
	for _, m := range members {
		var existing MatrixRoomMember
		err := conn.Where("room_id = ? AND user_id = ?", conv.RoomID, m.MatrixID).First(&existing).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		membership := existing.Membership
		// A voluntary leave after this membership generation is never undone
		// by an unrelated membership reconciliation.
		if membership == MembershipLeave && existing.UpdatedAt != nil && existing.UpdatedAt.UnixMilli() >= m.JoinedAt {
			continue
		}
		if membership == MembershipBan {
			return errDirectMessageSkipped
		}
		if membership != MembershipJoin && membership != MembershipInvite {
			if me := hs.Room.InviteUser(conv.RoomID, conv.Controller, &InviteUserRequest{UserID: m.MatrixID}); me != nil {
				return errors.New(me.Error)
			}
		}
		if m.Kind != "matrix" && membership != MembershipJoin {
			if _, me := hs.Room.JoinRoom(m.MatrixID, conv.RoomID, &JoinRoomRequest{}); me != nil {
				return errors.New(me.Error)
			}
		}
	}
	active := map[string]bool{conv.Controller: true}
	for _, m := range members {
		active[m.MatrixID] = true
	}
	var present []MatrixRoomMember
	if err := conn.Where("room_id = ? AND membership IN ?", conv.RoomID, []MembershipState{MembershipJoin, MembershipInvite}).Find(&present).Error; err != nil {
		return err
	}
	for _, m := range present {
		if !active[m.UserID] {
			if me := hs.Room.KickUser(conv.RoomID, conv.Controller, &KickUserRequest{UserID: m.UserID}); me != nil {
				return errors.New(me.Error)
			}
		}
	}
	return hs.syncBridgeRoomName(ctx, conv.RoomID)
}
func (hs *HomeServer) bridgeConversationAccess(tx *gorm.DB, id, userID string) (*BridgeConversation, *BridgeConversationMember, error) {
	var conv BridgeConversation
	if err := tx.First(&conv, "id = ?", id).Error; err != nil {
		return nil, nil, err
	}
	var member BridgeConversationMember
	if err := tx.Where("conversation_id = ? AND matrix_id = ? AND left_revision = 0", id, userID).First(&member).Error; err != nil {
		return nil, nil, err
	}
	var count int64
	if err := tx.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ? AND membership = ?", conv.RoomID, userID, MembershipJoin).Count(&count).Error; err != nil {
		return nil, nil, err
	}
	if count != 1 {
		return nil, nil, errDirectMessageSkipped
	}
	return &conv, &member, nil
}
func (hs *HomeServer) ChangeBridgeMembers(ctx context.Context, id, userID string, req BridgeConversationRequest, removeID uint64) error {
	conn := db.GetDB().WithContext(ctx)
	var conv *BridgeConversation
	err := conn.Transaction(func(tx *gorm.DB) error {
		var locked BridgeConversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&locked, "id = ?", id).Error; err != nil {
			return err
		}
		c, actor, err := hs.bridgeConversationAccess(tx, id, userID)
		if err != nil {
			return err
		}
		conv = c
		if c.Legacy {
			return errors.New("legacy private rooms cannot change audience; start an explicit new conversation")
		}
		if removeID > 0 {
			var target BridgeConversationMember
			if err := tx.First(&target, "id = ? AND conversation_id = ? AND left_revision = 0", removeID, id).Error; err != nil {
				return err
			}
			if actor.AccountID != c.CreatorAccountID && target.ID != actor.ID {
				return errDirectMessageSkipped
			}
			c.Revision++
			if err := tx.Model(&target).Update("left_revision", c.Revision).Error; err != nil {
				return err
			}
		} else {
			if actor.AccountID != c.CreatorAccountID || !req.PlaintextConsent || len(req.Members) == 0 {
				return errDirectMessageSkipped
			}
			var count int64
			if err := tx.Model(&BridgeConversationMember{}).Where("conversation_id = ? AND left_revision = 0", id).Count(&count).Error; err != nil {
				return err
			}
			if int(count)+len(req.Members) > 32 {
				return errDirectMessageSkipped
			}
			owner, err := hs.bridgeLocalAccount(tx, userID)
			if err != nil {
				return err
			}
			c.Revision++
			for _, input := range req.Members {
				m, err := hs.newBridgeMember(tx, c, input, owner)
				if err != nil {
					return err
				}
				var exists int64
				if err := tx.Model(&BridgeConversationMember{}).Where("conversation_id = ? AND matrix_id = ? AND left_revision = 0", id, m.MatrixID).Count(&exists).Error; err != nil {
					return err
				}
				if exists > 0 {
					continue
				}
				if err := tx.Create(&m).Error; err != nil {
					return err
				}
			}
		}
		return tx.Model(c).Updates(map[string]any{"revision": c.Revision, "state": "provisioning"}).Error
	})
	if err != nil {
		return err
	}

	return hs.provisionBridgeConversation(ctx, conv)
}

// Snapshot only joined, approved generations at event commit. The federation
// caller reaches here only after room-version signature/auth/state checks.
func (hs *HomeServer) queueConversationMessage(tx *gorm.DB, event *MatrixEvent) (bool, error) {
	var conv BridgeConversation
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("room_id = ? AND legacy = ?", event.RoomID, false).First(&conv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if conv.State != "active" {
		return true, errDirectMessageSkipped
	}
	if event.EventType != EventTypeRoomMessage || event.StateKey != nil {
		return true, nil
	}
	var members []BridgeConversationMember
	if err := tx.Where("conversation_id = ? AND left_revision = 0", conv.ID).Find(&members).Error; err != nil {
		return true, err
	}
	var ids, memberIDs []uint64
	var senderID uint64
	for _, m := range members {
		var joined int64
		if err := tx.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ? AND membership = ?", conv.RoomID, m.MatrixID, MembershipJoin).Count(&joined).Error; err != nil {
			return true, err
		}
		if joined == 0 {
			continue
		}
		if m.Kind == "matrix" && m.AccountID == 0 {
			a, err := hs.EnsureExternalMatrixActor(tx, m.MatrixID)
			if err != nil {
				return true, err
			}
			m.AccountID = a.Id
			if err := tx.Model(&m).Update("account_id", a.Id).Error; err != nil {
				return true, err
			}
		}
		memberIDs = append(memberIDs, m.ID)
		if m.MatrixID == event.Sender {
			senderID = m.AccountID
		} else {
			ids = append(ids, m.AccountID)
		}
	}
	if senderID == 0 || len(ids) == 0 {
		return true, errDirectMessageSkipped
	}
	job := MatrixDMOutbound{EventID: event.EventID, RoomID: event.RoomID, SenderAccountID: senderID, ConversationID: conv.ID, RecipientIDs: ids, MemberIDs: memberIDs, State: "pending", NextAttemptAt: time.Now()}
	_, recipients, err := hs.conversationOutboundAudience(tx, &job, event)
	if err != nil {
		return true, err
	}
	switch event.Content["msgtype"] {
	case "m.text", "m.notice", "m.emote":
	case "m.image", "m.video", "m.audio", "m.file":
		if err := hs.admitBridgeMedia(tx, event); err != nil {
			return true, err
		}
	default:
		return true, errDirectMessageSkipped
	}
	if _, _, err := hs.outboundReplyContent(tx, &job, event, &models.Status{}, recipients, true); err != nil {
		return true, err
	}
	return true, tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&job).Error
}
func (hs *HomeServer) conversationOutboundAudience(tx *gorm.DB, job *MatrixDMOutbound, event *MatrixEvent) (*models.Status, []models.Account, error) {
	var conv BridgeConversation
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND room_id = ?", job.ConversationID, job.RoomID).First(&conv).Error; err != nil {
		return nil, nil, err
	}
	if conv.State != "active" {
		return nil, nil, errors.New("conversation membership reconciliation pending")
	}
	var encryption int64
	if err := tx.Model(&MatrixRoomState{}).Where("room_id = ? AND event_type = ?", job.RoomID, EventTypeRoomEncryption).Count(&encryption).Error; err != nil {
		return nil, nil, err
	}
	if encryption != 0 {
		return nil, nil, errDirectMessageSkipped
	}
	var sender models.Account
	if err := tx.First(&sender, job.SenderAccountID).Error; err != nil {
		return nil, nil, err
	}
	if sender.IsBan {
		return nil, nil, errDirectMessageSkipped
	}
	var members []BridgeConversationMember
	if err := tx.Where("conversation_id = ? AND id IN ? AND left_revision = 0", conv.ID, job.MemberIDs).Find(&members).Error; err != nil {
		return nil, nil, err
	}
	if len(members) != len(job.MemberIDs) {
		return nil, nil, errDirectMessageSkipped
	}
	allowed := map[uint64]bool{}
	senderValid := false
	for _, m := range members {
		if m.Kind == "local" {
			if _, err := hs.bridgeLocalAccount(tx, m.MatrixID); err != nil {
				return nil, nil, err
			}
		}
		if m.AccountID == job.SenderAccountID && m.MatrixID == event.Sender {
			senderValid = true
			if m.Kind == "local" {
				if _, err := hs.bridgeLocalAccount(tx, event.Sender); err != nil {
					return nil, nil, err
				}
			}
		}
		var joined int64
		if err := tx.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ? AND membership = ?", conv.RoomID, m.MatrixID, MembershipJoin).Count(&joined).Error; err != nil {
			return nil, nil, err
		}
		if joined == 0 {
			return nil, nil, errDirectMessageSkipped
		}
		allowed[m.AccountID] = true
	}
	if !senderValid {
		return nil, nil, errDirectMessageSkipped
	}
	recipients := []models.Account{}
	for _, id := range job.RecipientIDs {
		if !allowed[id] || id == sender.Id {
			return nil, nil, errDirectMessageSkipped
		}
		var a models.Account
		if err := tx.First(&a, id).Error; err != nil {
			return nil, nil, err
		}
		blocked, err := models.AccountsBlocked(tx, &sender, &a)
		if err != nil {
			return nil, nil, err
		}
		if a.IsBan || blocked {
			return nil, nil, errDirectMessageSkipped
		}
		recipients = append(recipients, a)
	}
	var actual []MatrixRoomMember
	if err := tx.Where("room_id = ? AND membership = ?", conv.RoomID, MembershipJoin).Find(&actual).Error; err != nil {
		return nil, nil, err
	}
	var current []BridgeConversationMember
	if err := tx.Where("conversation_id = ? AND left_revision = 0", conv.ID).Find(&current).Error; err != nil {
		return nil, nil, err
	}
	currentIDs := map[string]bool{conv.Controller: true}
	for _, m := range current {
		currentIDs[m.MatrixID] = true
	}
	for _, m := range actual {
		if !currentIDs[m.UserID] {
			return nil, nil, errDirectMessageSkipped
		}
	}
	return &models.Status{}, recipients, nil
}

func bridgeConversationTitle(members []BridgeConversationMember) string {
	return fmt.Sprintf("私信 · %d 位成员", len(members))
}

// Membership invitations can wait on a remote server. Keep exactly one
// reconciler independent of message delivery, never one goroutine per room.
func (hs *HomeServer) StartBridgeConversationReconciler(ctx context.Context) {
	go func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				round, cancel := context.WithTimeout(ctx, 30*time.Second)
				hs.reconcileBridgeConversations(round)
				cancel()
			}
		}
	}()
}

func (hs *HomeServer) reconcileBridgeConversations(ctx context.Context) {
	var pending []BridgeConversation
	if db.GetDB().WithContext(ctx).Where("state = ? AND legacy = ?", "provisioning", false).Order("created_at").Limit(20).Find(&pending).Error != nil {
		return
	}
	for _, c := range pending {
		if ctx.Err() != nil {
			return
		}
		_ = hs.provisionBridgeConversation(ctx, &c)
	}
}

func (hs *HomeServer) bridgeEventAccount(tx *gorm.DB, roomID, matrixID string) (*models.Account, error) {
	var conv BridgeConversation
	err := tx.Where("room_id = ?", roomID).First(&conv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return hs.bridgeLocalAccount(tx, matrixID)
	}
	if err != nil {
		return nil, err
	}
	if conv.State != "active" {
		return nil, errDirectMessageSkipped
	}
	_, m, err := hs.bridgeConversationAccess(tx, conv.ID, matrixID)
	if err != nil {
		return nil, err
	}
	if m.Kind != "matrix" {
		return hs.bridgeLocalAccount(tx, matrixID)
	}
	if m.AccountID == 0 {
		a, err := hs.EnsureExternalMatrixActor(tx, matrixID)
		if err != nil {
			return nil, err
		}
		if err := tx.Model(m).Update("account_id", a.Id).Error; err != nil {
			return nil, err
		}
		return a, nil
	}
	var a models.Account
	if err := tx.First(&a, m.AccountID).Error; err != nil {
		return nil, err
	}
	if a.IsBan {
		return nil, errDirectMessageSkipped
	}
	return &a, nil
}

// syncBridgeMembershipDeparture runs only after an authenticated membership
// event has been selected as current room state, in that same SQL transaction.
// A native leave closes the generation permanently; a later join cannot revive
// a queued message's old member snapshot without a fresh explicit invitation.
func (hs *HomeServer) syncBridgeMembershipDeparture(tx *gorm.DB, event *MatrixEvent) error {
	if event == nil || event.EventType != EventTypeRoomMember || event.StateKey == nil || (!strings.HasPrefix(event.RoomID, "!apchat_") && !strings.HasPrefix(event.RoomID, "!apdm_")) {
		return nil
	}
	var conv BridgeConversation
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&conv, "room_id = ?", event.RoomID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if *event.StateKey == conv.Controller {
		return nil
	}
	membership, _ := event.Content["membership"].(string)
	var member BridgeConversationMember
	err = tx.Where("conversation_id = ? AND matrix_id = ? AND left_revision = 0", conv.ID, *event.StateKey).First(&member).Error
	if membership == string(MembershipJoin) {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errDirectMessageSkipped
		}
		return err
	}
	if membership != string(MembershipLeave) && membership != string(MembershipBan) {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	revision := conv.Revision + 1
	if err := tx.Model(&member).Where("left_revision = 0").Update("left_revision", revision).Error; err != nil {
		return err
	}
	return tx.Model(&conv).Update("revision", revision).Error
}
