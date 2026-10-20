package homeserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"io"
	"testing"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/storage"
	"github.com/stretchr/testify/require"
)

// unavailableStorage fails every read, like a backend that is not reachable
// yet right after a restart.
type unavailableStorage struct{ storage.Backend }

func (unavailableStorage) Open(context.Context, string) (io.ReadCloser, storage.ObjectInfo, error) {
	return nil, storage.ObjectInfo{}, errors.New("backend not ready")
}

func TestWebGroupMemberAvatarStorageFailureIsRetryable503(t *testing.T) {
	hs, router, roomID := webGroupMediaTestHS(t)
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	id := webGroupFixtureUploadID(t, uploadWebGroupFixture(t, router, roomID, "@alice:test.local", "photo.png", pngData.Bytes()))
	content, e := webGroupUploadContent(db.GetDB(), roomID, "@alice:test.local", id)
	require.Nil(t, e)
	alice := "@alice:test.local"
	_, e = hs.Event.SendRoomEvent(roomID, alice, "m.room.member", nil, map[string]any{"membership": "join", "avatar_url": content["url"]}, &alice)
	require.Nil(t, e)
	joinUser(t, hs, roomID, "@bob:test.local")
	memberAvatar := "/api/v1/chat/groups/" + webGroupID(roomID) + "/members/" + base64.RawURLEncoding.EncodeToString([]byte(alice)) + "/avatar"
	require.Equal(t, 200, webGroupFixtureDownload(router, memberAvatar, "@bob:test.local").Code)

	restore := storage.SetDefault(unavailableStorage{storage.Default()})
	resp := webGroupFixtureDownload(router, memberAvatar, "@bob:test.local")
	restore()
	require.Equal(t, 503, resp.Code, resp.Body.String())
	require.NotEmpty(t, resp.Header().Get("Retry-After"))
	require.Contains(t, resp.Body.String(), `"retryable":true`)

	// a local MXC with no media record is a plain 404, not a gateway error
	_, e = hs.Event.SendRoomEvent(roomID, alice, "m.room.member", nil, map[string]any{"membership": "join", "avatar_url": "mxc://test.local/doesnotexist"}, &alice)
	require.Nil(t, e)
	require.Equal(t, 404, webGroupFixtureDownload(router, memberAvatar, "@bob:test.local").Code)
}
