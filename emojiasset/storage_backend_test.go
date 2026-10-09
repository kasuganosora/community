package emojiasset

import (
	"bytes"
	"context"
	"net/http/httptest"
	"path"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/require"
)

func TestEmojiAssetsUseObjectStorage(t *testing.T) {
	for name, mk := range map[string]func(t *testing.T) storage.Backend{
		"fake": func(*testing.T) storage.Backend { return storagetest.NewMemory() },
		"s3": func(t *testing.T) storage.Backend {
			env := storagetest.S3FromEnv(t)
			return storage.NewS3Backend(env.Config(env.NewBucket(t)))
		},
	} {
		t.Run(name, func(t *testing.T) {
			assetWorkspace(t)
			b := mk(t)
			defer storage.SetDefault(b)()

			a, err := StoreEmojiImage(context.Background(), bytes.NewReader(samplePNG(t, 2, 2)), "image/png")
			require.NoError(t, err)
			for _, raw := range []string{a.URL, a.StaticURL} {
				_, err := b.Stat(context.Background(), "emojis/"+path.Base(raw))
				require.NoError(t, err, "emoji stored in the bucket")
			}

			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.GET("/emoji-assets/:file", Serve)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", strings.TrimPrefix(a.URL, "https://emoji.example"), nil))
			require.Equal(t, 200, w.Code)
			require.Equal(t, "image/png", w.Header().Get("Content-Type"))
			require.Equal(t, samplePNG(t, 2, 2), w.Body.Bytes())

			require.NoError(t, DeleteAsset(a))
			for _, raw := range []string{a.URL, a.StaticURL} {
				_, err := b.Stat(context.Background(), "emojis/"+path.Base(raw))
				require.ErrorIs(t, err, storage.ErrNotFound)
			}
			w = httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", strings.TrimPrefix(a.URL, "https://emoji.example"), nil))
			require.Equal(t, 404, w.Code)
		})
	}
}
