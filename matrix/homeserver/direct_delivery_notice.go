package homeserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// A notice is delivered only to a dedicated local author notification room.
// It is never inserted into the original private conversation and contains no
// message body, recipient names, or upstream errors. Transaction IDs make a
// crash after event commit but before checkpointing harmless.
func (hs *HomeServer) publishDirectDeliveryNotice(ctx context.Context, job *MatrixDMOutbound) error {
	state := job.DeliveryFeedbackState
	if state != "failed" && state != "skipped" && !(state == "delivered" && (job.DeliveryNoticeState == "failed" || job.DeliveryNoticeState == "skipped")) {
		return nil
	}
	if state == job.DeliveryNoticeState {
		return nil
	}
	conn := db.GetDB().WithContext(ctx)
	var author models.Account
	if err := conn.Preload("User").First(&author, job.SenderAccountID).Error; err != nil {
		return err
	}

	if author.IsBan {
		return errDirectMessageSkipped
	}
	external := false
	user := hs.bridgeAccountID(author)
	if author.UserId == 0 {
		var mapping MatrixExternalActor
		if err := conn.Where("account_id = ?", author.Id).First(&mapping).Error; err != nil {
			return err
		}
		user = mapping.MatrixID
		_, server, ok := ParseMatrixUserID(user)
		if !ok || server == hs.Config.ServerName {
			return errDirectMessageSkipped
		}
		external = true
	} else if author.Domain != nil || author.User == nil || author.User.IsBan || author.User.Status != models.UserStatusNormal {
		return errDirectMessageSkipped
	}
	digest := sha256.Sum256([]byte(user))
	key := hex.EncodeToString(digest[:])
	roomID := "!apnotice_" + key + ":" + hs.Config.ServerName
	bot := "@_ap_bridge_notice_" + key + ":" + hs.Config.ServerName
	var room MatrixRoom
	err := conn.Where("room_id = ?", roomID).First(&room).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		name := "Community 消息投递通知"
		topic := "仅你可见的消息发送状态。远端服务器接收不代表对方已读。"
		_, me := hs.Room.CreateRoom(bot, &CreateRoomRequest{bridgeRoomID: roomID, Visibility: "private", Preset: "private_chat", Name: &name, Topic: &topic, Invite: []string{user}, CreationContent: map[string]any{"m.federate": external}, PowerLevelContentOverride: &PowerLevelContent{Users: map[string]int64{bot: 100, user: 0}, EventsDefault: 100, StateDefault: 100, Invite: 100, Kick: 100, Ban: 100, Redact: 100}})
		if me != nil {
			// Another worker may have created this same deterministic room.
			if e := conn.Where("room_id = ? AND creator = ?", roomID, bot).First(&room).Error; e != nil {
				return errors.New(me.Error)
			}
		}
	} else if err != nil {
		return err
	} else if room.Creator != bot {
		return errDirectMessageSkipped
	}
	membership, err := hs.Room.GetUserMembership(roomID, user)
	if err != nil {
		return err
	}
	if membership == MembershipInvite {
		if external {
			return errors.New("notification room invitation awaits acceptance")
		}
		if _, me := hs.Room.JoinRoom(user, roomID, &JoinRoomRequest{}); me != nil {
			return errors.New(me.Error)
		}
	} else if membership != MembershipJoin {
		return errDirectMessageSkipped
	}
	// A deliberate leave is respected, and any unexpected audience fails closed.
	var members []MatrixRoomMember
	if err := conn.Where("room_id = ? AND membership IN ?", roomID, []MembershipState{MembershipJoin, MembershipInvite}).Find(&members).Error; err != nil {
		return err
	}
	for _, m := range members {
		if m.UserID != bot && m.UserID != user {
			return errDirectMessageSkipped
		}
	}
	var encrypted int64
	if err := conn.Model(&MatrixRoomState{}).Where("room_id = ? AND event_type = ?", roomID, EventTypeRoomEncryption).Count(&encrypted).Error; err != nil {
		return err
	}
	if encrypted != 0 {
		return errDirectMessageSkipped
	}
	message := "消息未能送达远端服务器。可在 Community 消息页面查看状态并重试。"
	if state == "skipped" {
		message = "消息已停止投递，请检查原会话的成员或访问权限。"
	}
	if state == "delivered" {
		message = "之前未完成的消息现已完成投递。服务器接收不代表对方已读。"
	}
	original := "https://matrix.to/#/" + url.PathEscape(job.RoomID) + "/" + url.PathEscape(job.EventID)
	website := strings.TrimRight(systemsettings.GetBaseURL(), "/") + "/messages"
	content := map[string]any{"msgtype": "m.notice", "body": message + "\n原消息：" + original + "\n查看投递：" + website}
	txnDigest := sha256.Sum256([]byte(job.EventID + "\x00" + state))
	txn := "delivery-notice-" + hex.EncodeToString(txnDigest[:])

	sent, me := hs.Event.SendRoomEvent(roomID, bot, string(EventTypeRoomMessage), &txn, content, nil)
	if me != nil {
		return fmt.Errorf("delivery notice unavailable: %s", me.Error)
	}
	if external {
		var event MatrixEvent
		if err := conn.Where("event_id = ? AND room_id = ?", sent.EventID, roomID).First(&event).Error; err != nil {
			return err
		}
		if err := hs.queueBridgeFederation(conn, &event); err != nil {
			return err
		}
	}

	return conn.Model(&MatrixDMOutbound{}).Where("event_id = ? AND delivery_feedback_state = ?", job.EventID, state).Update("delivery_notice_state", state).Error
}
