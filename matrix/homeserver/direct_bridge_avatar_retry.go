package homeserver

import (
	"context"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/defaultavatar"
)

// Cosmetic recovery is independent of message delivery: transient failures
// recover even when nobody sends another private message.
func (hs *HomeServer) startBridgeAvatarRetry(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		var cursor, roomCursor uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cursor = hs.retryBridgeAvatarBatch(ctx, cursor)
				roomCursor = hs.refreshBridgeRoomAvatarBatch(ctx, roomCursor)
			}
		}
	}()
}

// Cursor pagination prevents an unreachable avatar from starving later rooms.
func (hs *HomeServer) retryBridgeAvatarBatch(ctx context.Context, after uint64) uint64 {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	prefix := "mxc://" + hs.Config.ServerName + "/" + defaultavatar.MediaPrefix
	pattern := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(prefix) + "%"
	remotePattern := "%!_ap:" + strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(hs.Config.ServerName)
	conn := db.GetDB().WithContext(ctx)
	var members []MatrixRoomMember
	if err := conn.Where("id > ? AND membership = ? AND avatar_url LIKE ? ESCAPE '!' AND user_id LIKE ? ESCAPE '!'", after, MembershipJoin, pattern, remotePattern).Order("id").Limit(10).Find(&members).Error; err != nil {
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
		account, err := hs.resolveBridgeAccount(conn, member.UserID)
		if err != nil || account == nil || account.Domain == nil {
			continue
		}
		// Recheck room state: concurrent delivery may already have restored the
		// true avatar, or this member may have left after the batch was selected.
		var state MatrixRoomState
		if err := conn.Where("room_id = ? AND event_type = ? AND state_key = ?", member.RoomID, EventTypeRoomMember, member.UserID).First(&state).Error; err != nil {
			continue
		}
		avatar, _ := state.Content["avatar_url"].(string)
		if state.Content["membership"] != "join" || !strings.HasPrefix(avatar, prefix) {
			continue
		}
		_ = hs.syncBridgeProfile(ctx, ctx, member.RoomID, member.UserID, *account)
	}
	return after
}
