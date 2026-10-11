package homeserver

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type bridgeAvatarTransport func(*http.Request) (*http.Response, error)

func (f bridgeAvatarTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func bridgeAvatarPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, img))
	return b.Bytes()
}
func TestDirectBridgeAvatarUpdatesExistingRoomAndLocalDefaults(t *testing.T) {
	hs, conn := directBridgeDB(t)
	restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
	defer restore()
	seedDirectBridge(t, conn, 101, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 101))
	var first models.MatrixDMDelivery
	require.NoError(t, conn.First(&first, "status_id = ?", 101).Error)
	body := bridgeAvatarPNG(t)
	old := newBridgeAvatarHTTPClient
	defer func() { newBridgeAvatarHTTPClient = old }()
	requests := 0
	newBridgeAvatarHTTPClient = func() *http.Client {
		return &http.Client{Transport: bridgeAvatarTransport(func(r *http.Request) (*http.Response, error) {
			requests++
			require.Equal(t, "https://remote.example/avatar.png", r.URL.String())
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}, nil
		})}
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 1).UpdateColumns(map[string]any{"avatar": "https://remote.example/avatar.png", "display_name": "Luna"}).Error)
	_, err := storage.Save("avatars/local.png", body)
	require.NoError(t, err)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 2).UpdateColumn("avatar", "/avatars/local.png").Error)
	seedDirectBridge(t, conn, 102, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 102))
	var second models.MatrixDMDelivery
	require.NoError(t, conn.First(&second, "status_id = ?", 102).Error)
	require.Equal(t, first.RoomID, second.RoomID)
	var remote models.Account
	require.NoError(t, conn.First(&remote, 1).Error)
	ghost := hs.bridgeAccountID(remote)
	for _, id := range []string{ghost, "@alice:test.local"} {
		var state MatrixRoomState
		require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", first.RoomID, EventTypeRoomMember, id).First(&state).Error)
		avatar, ok := state.Content["avatar_url"].(string)
		require.True(t, ok)
		require.True(t, strings.HasPrefix(avatar, "mxc://test.local/"))
		media, reader, err := NewMediaService(hs).GetMedia("test.local", strings.TrimPrefix(avatar, "mxc://test.local/"))
		require.NoError(t, err)
		imageBytes, err := io.ReadAll(reader)
		reader.Close()
		require.NoError(t, err)
		require.Equal(t, "image/png", media.ContentType)
		_, err = png.Decode(bytes.NewReader(imageBytes))
		require.NoError(t, err)
	}
	require.Equal(t, 1, requests)
	var before int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", first.RoomID, EventTypeRoomMember).Count(&before).Error)
	seedDirectBridge(t, conn, 103, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 103))
	require.Equal(t, 1, requests, "unchanged profile must not redownload per message")
	var after int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", first.RoomID, EventTypeRoomMember).Count(&after).Error)
	require.Equal(t, before, after)
	profile, ok := hs.bridgeGhostProfile(context.Background(), ghost)
	require.True(t, ok)
	require.Contains(t, profile["displayname"], "Luna")
	require.True(t, strings.HasPrefix(profile["avatar_url"].(string), "mxc://"))
	// Explicit Matrix selection must win even when the old Community static avatar exists.
	chosen := "mxc://test.local/user-selected"
	propagateProfileChange(hs, "@alice:test.local", "avatar_url", chosen)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 2).UpdateColumns(map[string]any{"avatar": chosen, "avatar_static": "/avatars/local.png"}).Error)
	seedDirectBridge(t, conn, 104, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 104))
	var member MatrixRoomState
	require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", first.RoomID, EventTypeRoomMember, "@alice:test.local").First(&member).Error)
	require.Equal(t, chosen, member.Content["avatar_url"])
	// Explicit removal in Matrix is also a user choice, not a request to
	// restore the old Community default on the next AP message.
	propagateProfileChange(hs, "@alice:test.local", "avatar_url", "")
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 2).UpdateColumn("avatar", "").Error)
	newBridgeAvatarHTTPClient = func() *http.Client {
		return &http.Client{Transport: bridgeAvatarTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable")), Header: make(http.Header)}, nil
		})}
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 1).UpdateColumn("avatar", "https://remote.example/temporarily-unavailable.png").Error)
	seedDirectBridge(t, conn, 105, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 105))
	member = MatrixRoomState{}
	require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", first.RoomID, EventTypeRoomMember, "@alice:test.local").First(&member).Error)
	require.Equal(t, hs.defaultBridgeAvatar(models.Account{Username: "alice"}), member.Content["avatar_url"])
	var ghostMember MatrixRoomState
	require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", first.RoomID, EventTypeRoomMember, ghost).First(&ghostMember).Error)
	require.True(t, strings.HasPrefix(ghostMember.Content["avatar_url"].(string), "mxc://"), "unavailable avatar retains prior valid image without blocking DM")
	router := gin.New()
	router.GET("/profile/:userId", handleGetProfile)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/profile/"+ghost, nil))
	require.Equal(t, 200, response.Code)
	require.Contains(t, response.Body.String(), "mxc://")
}

func TestBridgeAvatarRejectsPrivateURLsAndAttachmentPaths(t *testing.T) {
	hs, _ := directBridgeDB(t)
	restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
	defer restore()
	_, err := storage.Save("matrix/private.png", bridgeAvatarPNG(t))
	require.NoError(t, err)
	_, err = storage.Save("media/private.png", bridgeAvatarPNG(t))
	require.NoError(t, err)
	for _, source := range []string{"http://remote.example/a.png", "https://127.0.0.1/avatar.png", "https://169.254.169.254/latest/meta-data/", "/matrix/private.png", "/media/private.png", "/avatars/../matrix/private.png", "data:image/png;base64,AAAA"} {
		_, err := hs.importBridgeAvatar(context.Background(), "@alice:test.local", source, true)
		require.Error(t, err, source)
	}
}

func TestDefaultBridgeAvatarVirtualMedia(t *testing.T) {
	hs, conn := directBridgeDB(t)
	a := models.Account{Username: "CaseSensitive"}
	raw := hs.defaultBridgeAvatar(a)
	mediaID := strings.TrimPrefix(raw, "mxc://"+hs.Config.ServerName+"/")
	media, reader, err := NewMediaService(hs).GetMedia(hs.Config.ServerName, mediaID)
	require.NoError(t, err)
	defer reader.Close()
	image, err := png.Decode(reader)
	require.NoError(t, err)
	require.Equal(t, 128, image.Bounds().Dx())
	require.Equal(t, "image/png", media.ContentType)
	var count int64
	require.NoError(t, conn.Model(&MatrixMedia{}).Count(&count).Error)
	require.Zero(t, count)
	_, _, err = NewMediaService(hs).GetMedia(hs.Config.ServerName, mediaID+"/../private")
	require.Error(t, err)
	_, _, err = NewMediaService(hs).GetMedia("remote.example", mediaID)
	require.Error(t, err)
	other := "elsewhere.example"
	a.Domain = &other
	require.NotEqual(t, raw, hs.defaultBridgeAvatar(a))
}

func TestBridgeGeneratedFallbackRetriesUnavailableLocalAvatar(t *testing.T) {
	hs, conn := directBridgeDB(t)
	restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
	defer restore()
	seedDirectBridge(t, conn, 201, 2)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 2).UpdateColumn("avatar", "/avatars/retry.png").Error)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 201))
	var delivery models.MatrixDMDelivery
	require.NoError(t, conn.First(&delivery, "status_id = ?", 201).Error)
	var member MatrixRoomState
	require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", delivery.RoomID, EventTypeRoomMember, "@alice:test.local").First(&member).Error)
	fallback := hs.defaultBridgeAvatar(models.Account{Username: "alice"})
	require.Equal(t, fallback, member.Content["avatar_url"])
	data := bridgeAvatarPNG(t)
	require.NoError(t, storage.Default().Put(context.Background(), "avatars/retry.png", bytes.NewReader(data), int64(len(data)), "image/png"))
	seedDirectBridge(t, conn, 202, 2)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 202))
	member = MatrixRoomState{}
	require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", delivery.RoomID, EventTypeRoomMember, "@alice:test.local").First(&member).Error)
	require.NotEqual(t, fallback, member.Content["avatar_url"])
	require.True(t, strings.HasPrefix(member.Content["avatar_url"].(string), "mxc://test.local/"))
}
