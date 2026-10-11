package homeserver

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBridgeAvatarUsesCurrentImageAndFallsBackToStatic(t *testing.T) {
	for _, failOriginal := range []bool{false, true} {
		t.Run(map[bool]string{false: "prefer-current-over-stale-static", true: "original-unavailable-use-static"}[failOriginal], func(t *testing.T) {
			hs, conn := directBridgeDB(t)
			restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
			defer restore()
			original := newBridgeAvatarHTTPClient
			defer func() { newBridgeAvatarHTTPClient = original }()
			data := bridgeAvatarPNG(t)
			var paths []string
			newBridgeAvatarHTTPClient = func() *http.Client {
				return &http.Client{Transport: bridgeAvatarTransport(func(r *http.Request) (*http.Response, error) {
					paths = append(paths, r.URL.Path)
					if failOriginal && r.URL.Path == "/current" {
						return &http.Response{StatusCode: 404, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("missing"))}, nil
					}
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(data))}, nil
				})}
			}
			require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 1).UpdateColumns(map[string]any{"avatar": "https://remote.example/current", "avatar_static": "https://remote.example/static"}).Error)
			seedDirectBridge(t, conn, 810, 2)
			require.NoError(t, hs.ProcessDirectMessage(context.Background(), 810))
			expected := []string{"/current"}
			if failOriginal {
				expected = append(expected, "/static")
			}
			require.Equal(t, expected, paths)
			var remote models.Account
			require.NoError(t, conn.First(&remote, 1).Error)
			var member MatrixRoomMember
			require.NoError(t, conn.Where("user_id = ? AND membership = ?", hs.bridgeAccountID(remote), MembershipJoin).First(&member).Error)
			require.NotNil(t, member.AvatarURL)
			require.NotEqual(t, hs.defaultBridgeAvatar(remote), *member.AvatarURL)
			require.True(t, strings.HasPrefix(*member.AvatarURL, "mxc://test.local/"))
		})
	}
}

func TestAvatarDiagnosticsDoNotExposeSignedURLs(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://cdn.example/image?token=secret-value", Err: errors.New("secret-value rejected")}
	reason := bridgeAvatarFailureReason(err)
	require.NotContains(t, reason, "secret-value")
	require.NotContains(t, reason, "cdn.example")
	require.Equal(t, "timeout_or_canceled", bridgeAvatarFailureReason(context.DeadlineExceeded))
}

func TestBridgeAvatarDownsizesLargeOriginalBeforePNGEncoding(t *testing.T) {
	hs, conn := directBridgeDB(t)
	restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
	defer restore()
	original := newBridgeAvatarHTTPClient
	defer func() { newBridgeAvatarHTTPClient = original }()
	source := image.NewRGBA(image.Rect(0, 0, 1200, 800))
	for y := 0; y < 800; y++ {
		for x := 0; x < 1200; x++ {
			source.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x * y), A: 255})
		}
	}
	var encoded bytes.Buffer
	require.NoError(t, jpeg.Encode(&encoded, source, &jpeg.Options{Quality: 85}))
	newBridgeAvatarHTTPClient = func() *http.Client {
		return &http.Client{Transport: bridgeAvatarTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(encoded.Bytes()))}, nil
		})}
	}
	mxc, err := hs.importBridgeAvatar(context.Background(), "@remote_remote_example_ap:test.local", "https://remote.example/photo.jpg", false)
	require.NoError(t, err)
	var media MatrixMedia
	require.NoError(t, conn.First(&media, "media_id = ?", strings.TrimPrefix(mxc, "mxc://test.local/")).Error)
	require.Equal(t, 512, *media.Width)
	require.Equal(t, 341, *media.Height)
	require.Less(t, media.FileSize, int64(bridgeAvatarMaxBytes))
}
