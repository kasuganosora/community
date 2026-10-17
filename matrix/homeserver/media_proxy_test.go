package homeserver

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/require"
)

type observedProxyBody struct {
	io.Reader
	reads  int
	closed bool
}

func (b *observedProxyBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (b *observedProxyBody) Close() error               { b.closed = true; return nil }

func TestMatrixAPProxyStreamsWithoutStorageAndHonorsCancellation(t *testing.T) {
	store := storagetest.NewMemory()
	store.Fail = errors.New("storage must not be used")
	t.Cleanup(storage.SetDefault(store))
	body := &observedProxyBody{Reader: strings.NewReader("payload")}
	var request *http.Request
	client := &http.Client{Transport: bridgeMediaRoundTrip(func(r *http.Request) (*http.Response, error) {
		request = r
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("Cookie"))
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"image/png"}}, ContentLength: -1, Body: body}, nil
	})}
	remote, err := openBridgeAttachment(context.Background(), "https://remote.example/private-image", client)
	require.NoError(t, err)
	require.Zero(t, body.reads, "proxy returns headers without buffering the whole file")
	require.EqualValues(t, -1, remote.Length)
	got, err := io.ReadAll(remote.Body)
	require.NoError(t, err)
	require.Equal(t, "payload", string(got))
	require.NoError(t, remote.Close())
	require.True(t, body.closed)
	require.ErrorIs(t, request.Context().Err(), context.Canceled)
	for _, raw := range []string{"http://remote.example/file", "file:///etc/passwd", "https://user:pass@remote.example/file", "https://remote.example/file#fragment"} {
		_, err := openBridgeAttachment(context.Background(), raw, client)
		require.Error(t, err)
	}
}

func TestMatrixAPProxyRejectsOversizedAndUnsafeResponses(t *testing.T) {
	for _, test := range []struct {
		status  int
		size    int64
		typ     string
		wantErr bool
	}{
		{200, matrixMediaLimit + 1, "image/png", true}, {302, 0, "text/plain", true}, {404, 0, "text/plain", true}, {200, 4, "text/html", false},
	} {
		body := &observedProxyBody{Reader: strings.NewReader("data")}
		client := &http.Client{Transport: bridgeMediaRoundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: test.status, ContentLength: test.size, Header: http.Header{"Content-Type": []string{test.typ}}, Body: body}, nil
		})}
		remote, err := openBridgeAttachment(context.Background(), "https://remote.example/file", client)
		if test.wantErr {
			require.Error(t, err)
			require.True(t, body.closed)
		} else {
			require.NoError(t, err)
			require.Equal(t, "application/octet-stream", remote.ContentType)
			remote.Close()
		}
	}
}

func TestMatrixMediaOriginsAndBridgeRemoteMappingDoNotDuplicateBytes(t *testing.T) {
	conn, store, _ := mediaGCFixture(t)
	hs := &HomeServer{Config: &HSConfig{ServerName: "test.local"}}
	service := NewMediaService(hs)
	local, err := service.UploadMedia(strings.NewReader("local"), &multipart.FileHeader{Filename: "local.txt"}, "text/plain", "@alice:test.local")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(local.FilePath, "matrix/local/"))
	require.False(t, local.GCEligible)
	remote, err := service.uploadMedia(strings.NewReader("remote"), nil, "text/plain", "", "remote", true)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(remote.FilePath, "matrix/remote/"))
	require.True(t, remote.GCEligible)
	mapping := &MatrixMedia{RemoteURL: "https://remote.example/file", Origin: "remote", FileName: "file.png", FileSize: 4, ContentType: "image/png"}
	store.Fail = errors.New("proxy mappings must not copy bytes")
	attachment, err := hs.outboundDirectAttachmentWithMedia(conn, &MatrixDMOutbound{EventID: "$proxy", SenderAccountID: 42}, &MatrixEvent{}, mapping)
	require.NoError(t, err)
	require.Equal(t, mapping.RemoteURL, attachment.RemoteUrl)
	require.Empty(t, attachment.FilePath)
	require.Equal(t, models.MediaTypeImage, attachment.Type)
}

func TestRemoteMatrixBridgeCreatesOnlyOneDeliverableCopy(t *testing.T) {
	conn, store, _ := mediaGCFixture(t)
	hs := &HomeServer{Config: &HSConfig{ServerName: "test.local"}}
	m := &MatrixMedia{Origin: "remote", FileName: "file.png", FileSize: 4, ContentType: "image/png", transientData: []byte("data")}
	attachment, err := hs.outboundDirectAttachmentWithMedia(conn, &MatrixDMOutbound{EventID: "$remote", SenderAccountID: 42}, &MatrixEvent{}, m)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(attachment.StorageKey(), "media/remote/matrix-direct/"))
	var keys []string
	require.NoError(t, store.List(context.Background(), "", func(info storage.ObjectInfo) error { keys = append(keys, info.Key); return nil }))
	require.Equal(t, []string{attachment.StorageKey()}, keys, "no persistent intermediate bridge object")
}

func TestMatrixProxyDoesNotAdvertiseUnknownMetadataAsZeroBytes(t *testing.T) {
	content := bridgeAttachmentContent("test.local", models.MediaAttachment{Type: models.MediaTypeImage}, MatrixMedia{MediaID: "proxy"}, false)
	info := content["info"].(map[string]any)
	require.NotContains(t, info, "size")
	require.NotContains(t, info, "mimetype")
}

func TestDirectBridgeRemoteAttachmentUsesProxyEvenWithLegacyCache(t *testing.T) {
	hs, conn := directBridgeDB(t)
	store := storagetest.NewMemory()
	store.Fail = errors.New("no attachment storage access is expected")
	t.Cleanup(storage.SetDefault(store))
	var statusID uint64 = 80
	seedDirectBridge(t, conn, statusID, 2)
	attachment := models.MediaAttachment{Id: 81, AccountId: 1, StatusId: &statusID, FileName: "remote.png", FilePath: "legacy-cached-copy.png", RemoteUrl: "https://unreachable.invalid/remote.png", Type: models.MediaTypeImage, ContentType: "image/png"}
	require.NoError(t, conn.Create(&attachment).Error)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), statusID))
	var media MatrixMedia
	require.NoError(t, conn.Where("remote_url = ?", attachment.RemoteUrl).First(&media).Error)
	require.Empty(t, media.FilePath)
	require.Equal(t, "remote", media.Origin)
	require.True(t, media.GCEligible)
	var events []MatrixEvent
	require.NoError(t, conn.Where("content LIKE ?", "%mxc://test.local/"+media.MediaID+"%").Find(&events).Error)
	require.Len(t, events, 1)
}
