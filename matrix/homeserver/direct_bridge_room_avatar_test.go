package homeserver

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBridgeRoomAvatarFollowsRemoteAndRepairsWithoutMessage(t *testing.T) {
	hs, conn := directBridgeDB(t)
	ctx := context.Background()
	seedDirectBridge(t, conn, 801, 2)
	require.NoError(t, hs.ProcessDirectMessage(ctx, 801))
	var delivery models.MatrixDMDelivery
	require.NoError(t, conn.First(&delivery, "status_id = ?", 801).Error)
	var account models.Account
	require.NoError(t, conn.First(&account, 1).Error)
	ghost := hs.bridgeAccountID(account)
	read := func() string {
		var state MatrixRoomState
		require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", delivery.RoomID, "m.room.avatar", "").First(&state).Error)
		url, _ := state.Content["url"].(string)
		return url
	}
	require.Equal(t, hs.defaultBridgeAvatar(account), read())
	var room MatrixRoom
	require.NoError(t, conn.First(&room, "room_id = ?", delivery.RoomID).Error)
	key := ""
	// Deliberate manual room overrides cannot diverge from this person's face.
	_, me := hs.Event.SendRoomEvent(delivery.RoomID, room.Creator, "m.room.avatar", nil, map[string]any{"url": "mxc://test.local/manual"}, &key)
	require.Nil(t, me)
	var member MatrixRoomState
	require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", delivery.RoomID, EventTypeRoomMember, ghost).First(&member).Error)
	member.Content["avatar_url"] = "mxc://test.local/remote-real"
	_, me = hs.Event.SendRoomEvent(delivery.RoomID, ghost, string(EventTypeRoomMember), nil, member.Content, &ghost)
	require.Nil(t, me)
	require.NoError(t, hs.syncBridgeRoomAvatar(ctx, delivery.RoomID))
	require.Equal(t, "mxc://test.local/remote-real", read())
	var before int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", delivery.RoomID, "m.room.avatar").Count(&before).Error)
	require.NoError(t, hs.syncBridgeRoomAvatar(ctx, delivery.RoomID))
	var after int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", delivery.RoomID, "m.room.avatar").Count(&after).Error)
	require.Equal(t, before, after)
	// A subsequent real profile change propagates without any redownload here.
	member.Content["avatar_url"] = "mxc://test.local/remote-new"
	_, me = hs.Event.SendRoomEvent(delivery.RoomID, ghost, string(EventTypeRoomMember), nil, member.Content, &ghost)
	require.Nil(t, me)
	require.NoError(t, hs.syncBridgeRoomAvatar(ctx, delivery.RoomID))
	require.Equal(t, "mxc://test.local/remote-new", read())
	// Converting into a group removes the auto-selected person's avatar.
	require.Nil(t, hs.Room.InviteUser(delivery.RoomID, room.Creator, &InviteUserRequest{UserID: "@bob:test.local"}))
	require.NoError(t, hs.syncBridgeRoomAvatar(ctx, delivery.RoomID))
	require.Empty(t, read())
	_, me = hs.Event.SendRoomEvent(delivery.RoomID, room.Creator, "m.room.avatar", nil, map[string]any{"url": "mxc://test.local/group"}, &key)
	require.Nil(t, me)
	require.NoError(t, hs.syncBridgeRoomAvatar(ctx, delivery.RoomID))
	require.Equal(t, "mxc://test.local/group", read())
}

func TestBridgeRoomAvatarRefreshesChangedSourceWithoutMessage(t *testing.T) {
	hs, conn := directBridgeDB(t)
	restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
	defer restore()
	old := newBridgeAvatarHTTPClient
	defer func() { newBridgeAvatarHTTPClient = old }()
	body := bridgeAvatarPNG(t)
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{G: 255, A: 255})
	var changed bytes.Buffer
	require.NoError(t, png.Encode(&changed, img))
	requests := 0
	newBridgeAvatarHTTPClient = func() *http.Client {
		return &http.Client{Transport: bridgeAvatarTransport(func(r *http.Request) (*http.Response, error) {
			requests++
			data := body
			if r.URL.Path == "/new.png" {
				data = changed.Bytes()
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(data)), Body: io.NopCloser(bytes.NewReader(data))}, nil
		})}
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 1).UpdateColumn("avatar", "https://remote.example/old.png").Error)
	seedDirectBridge(t, conn, 802, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 802))
	var delivery models.MatrixDMDelivery
	require.NoError(t, conn.First(&delivery, "status_id = ?", 802).Error)
	read := func() string {
		var state MatrixRoomState
		require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", delivery.RoomID, "m.room.avatar", "").First(&state).Error)
		return state.Content["url"].(string)
	}
	original := read()
	hs.refreshBridgeRoomAvatarBatch(context.Background(), 0)
	require.Equal(t, 1, requests)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 1).UpdateColumn("avatar", "https://remote.example/new.png").Error)
	hs.refreshBridgeRoomAvatarBatch(context.Background(), 0)
	require.NotEqual(t, original, read())
	require.Equal(t, 2, requests)
	hs.refreshBridgeRoomAvatarBatch(context.Background(), 0)
	require.Equal(t, 2, requests)
	var messages int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", delivery.RoomID, "m.room.message").Count(&messages).Error)
	require.EqualValues(t, 1, messages)
}
