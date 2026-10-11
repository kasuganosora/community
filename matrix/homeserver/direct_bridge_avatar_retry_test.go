package homeserver

import (
	"bytes"
	"context"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBridgeAvatarRetryRecoversWithoutAnotherMessage(t *testing.T) {
	hs, conn := directBridgeDB(t)
	restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
	defer restore()
	seedDirectBridge(t, conn, 701, 2)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 1).UpdateColumn("avatar", "https://remote.example/retry.png").Error)
	body := bridgeAvatarPNG(t)
	available := false
	requests := 0
	old := newBridgeAvatarHTTPClient
	defer func() { newBridgeAvatarHTTPClient = old }()
	newBridgeAvatarHTTPClient = func() *http.Client {
		return &http.Client{Transport: bridgeAvatarTransport(func(r *http.Request) (*http.Response, error) {
			requests++
			if !available {
				return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body))}, nil
		})}
	}
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 701))
	var account models.Account
	require.NoError(t, conn.First(&account, 1).Error)
	ghost := hs.bridgeAccountID(account)
	var member MatrixRoomMember
	require.NoError(t, conn.Where("user_id = ?", ghost).First(&member).Error)
	require.Equal(t, hs.defaultBridgeAvatar(account), *member.AvatarURL)
	chosen := "mxc://test.local/manual-selection"
	propagateProfileChange(hs, "@alice:test.local", "avatar_url", chosen)
	available = true
	hs.retryBridgeAvatarBatch(context.Background(), 0)
	require.NoError(t, conn.First(&member, member.ID).Error)
	require.NotEqual(t, hs.defaultBridgeAvatar(account), *member.AvatarURL)
	_, reader, err := NewMediaService(hs).GetMedia("test.local", strings.TrimPrefix(*member.AvatarURL, "mxc://test.local/"))
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	reader.Close()
	require.NoError(t, err)
	require.Equal(t, body, data)
	var local MatrixRoomState
	require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", member.RoomID, EventTypeRoomMember, "@alice:test.local").First(&local).Error)
	require.Equal(t, chosen, local.Content["avatar_url"])
	before := requests
	hs.retryBridgeAvatarBatch(context.Background(), 0)
	require.Equal(t, before, requests, "successful avatar must not redownload")
	var messages int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", member.RoomID, "m.room.message").Count(&messages).Error)
	require.EqualValues(t, 1, messages, "recovery must not require another message")
}

func TestBridgeAvatarRetryCancelledAndDepartedMembers(t *testing.T) {
	hs, conn := directBridgeDB(t)
	seedDirectBridge(t, conn, 702, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 702))
	var account models.Account
	require.NoError(t, conn.First(&account, 1).Error)
	ghost := hs.bridgeAccountID(account)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 1).UpdateColumn("avatar", "https://remote.example/retry.png").Error)
	var member MatrixRoomMember
	require.NoError(t, conn.Where("user_id = ?", ghost).First(&member).Error)
	old := newBridgeAvatarHTTPClient
	defer func() { newBridgeAvatarHTTPClient = old }()
	newBridgeAvatarHTTPClient = func() *http.Client { t.Fatal("cancelled or departed member must not download"); return nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.EqualValues(t, 19, hs.retryBridgeAvatarBatch(ctx, 19))
	require.NoError(t, conn.Model(&member).Update("membership", MembershipLeave).Error)
	hs.retryBridgeAvatarBatch(context.Background(), 0)
}
