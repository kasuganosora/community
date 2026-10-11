package homeserver

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/defaultavatar"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

const bridgeGeneratedRoomAvatarKey = "org.community.generated_avatar"

// A bridged one-to-one chat represents its remote participant. Reuse that
// participant's published MXC, avoiding another network fetch or media copy.
func (hs *HomeServer) syncBridgeRoomAvatar(ctx context.Context, roomID string) error {
	conn := db.GetDB().WithContext(ctx)
	var room MatrixRoom
	if err := conn.First(&room, "room_id = ?", roomID).Error; err != nil {
		return err
	}
	var conv BridgeConversation
	err := conn.First(&conv, "room_id = ?", roomID).Error
	if err == nil {
		if conv.Controller != room.Creator || conv.State != "active" {
			return nil
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		suffix := ":" + hs.Config.ServerName
		hash := strings.TrimSuffix(strings.TrimPrefix(roomID, "!apdm_"), suffix)
		if !strings.HasPrefix(roomID, "!apdm_") || len(hash) != 64 || room.Creator != "@_ap_bridge_"+hash+suffix {
			return nil
		}
		var count int64
		if err := conn.Model(&models.MatrixDMDelivery{}).Where("room_id = ? AND state = ?", roomID, "delivered").Limit(1).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
	} else {
		return err
	}
	var current MatrixRoomState
	err = conn.Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, "m.room.avatar", "").First(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	currentURL, _ := current.Content["url"].(string)
	generated, _ := current.Content[bridgeGeneratedRoomAvatarKey].(string)
	var members []MatrixRoomMember
	if err := conn.Where("room_id = ? AND user_id <> ? AND membership IN ?", roomID, room.Creator, []MembershipState{MembershipJoin, MembershipInvite}).Limit(3).Find(&members).Error; err != nil {
		return err
	}
	desired := ""
	if len(members) == 2 {
		remoteCount := 0
		for _, member := range members {
			account, err := hs.resolveBridgeAccount(conn, member.UserID)
			if err != nil {
				return nil
			}
			if account == nil {
				continue
			}
			remoteCount++
			desired = hs.defaultBridgeAvatar(*account)
			var profile MatrixRoomState
			if err := conn.Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, EventTypeRoomMember, member.UserID).First(&profile).Error; err != nil {
				return err
			}
			if avatar, ok := profile.Content["avatar_url"].(string); ok && validBridgeMXC(avatar) {
				desired = avatar
			}
		}
		if remoteCount != 1 {
			desired = ""
		}
	}
	if desired == "" {
		// A group must not keep displaying one person's automatically chosen face.
		// A subsequently chosen group avatar remains a group setting.
		if generated == "" || currentURL != generated {
			return nil
		}
	} else if currentURL == desired && generated == desired {
		return nil
	}
	content := map[string]any{}
	if desired != "" {
		content["url"] = desired
		content[bridgeGeneratedRoomAvatarKey] = desired
	}
	key := ""
	if _, me := hs.Event.SendRoomEvent(roomID, room.Creator, "m.room.avatar", nil, content, &key); me != nil {
		return errors.New(me.Error)
	}
	return nil
}

// Repair already-imported avatars too, independently of retrying downloads.
func (hs *HomeServer) refreshBridgeRoomAvatarBatch(ctx context.Context, after uint64) uint64 {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pattern := "%!_ap:" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(hs.Config.ServerName)
	var members []MatrixRoomMember
	if err := db.GetDB().WithContext(ctx).Where("id > ? AND membership = ? AND user_id LIKE ? ESCAPE '!'", after, MembershipJoin, pattern).Order("id").Limit(10).Find(&members).Error; err != nil {
		return after
	}
	if len(members) == 0 {
		return 0
	}
	for _, member := range members {
		if ctx.Err() != nil {
			return after
		}
		after = member.ID
		// Successful imports also need to follow later actor profile updates.
		// syncBridgeProfile fingerprints sources, so unchanged images incur no
		// download. Default-image retries belong to the separate retry pass.
		conn := db.GetDB().WithContext(ctx)
		account, err := hs.resolveBridgeAccount(conn, member.UserID)
		if err == nil && account != nil {
			var state MatrixRoomState
			if conn.Where("room_id = ? AND event_type = ? AND state_key = ?", member.RoomID, EventTypeRoomMember, member.UserID).First(&state).Error == nil {
				avatar, _ := state.Content["avatar_url"].(string)
				fallback := "mxc://" + hs.Config.ServerName + "/" + defaultavatar.MediaPrefix
				if state.Content["membership"] == "join" && validBridgeMXC(avatar) && !strings.HasPrefix(avatar, fallback) {
					_ = hs.syncBridgeProfile(ctx, ctx, member.RoomID, member.UserID, *account)
				}
			}
		}
		_ = hs.syncBridgeRoomAvatar(ctx, member.RoomID)
	}
	return after
}
