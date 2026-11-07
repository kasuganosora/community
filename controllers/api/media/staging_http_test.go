package media

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/linkcard"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/stretchr/testify/require"
)

func TestPrivateStagingIsUnreachableOverHTTP(t *testing.T) {
	withTempWorkDir(t)
	gdb := setupMediaTestDB(t)
	restore := storage.SetDefault(&storage.LocalBackend{Root: "uploads"})
	t.Cleanup(restore)

	secret := []byte("STAGING-SECRET-TOKEN")
	staging := filepath.Join("uploads", ".private-staging", "abc")
	require.NoError(t, os.MkdirAll(staging, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(staging, "input"), secret, 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join("uploads", "media"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join("..", ".private-staging", "abc", "input"), filepath.Join("uploads", "media", "link.webp")))
	require.NoError(t, os.Symlink(filepath.Join("..", ".private-staging", "abc"), filepath.Join("uploads", "media", "aliasdir")))
	outside := filepath.Join("outside-secret")
	require.NoError(t, os.WriteFile(outside, secret, 0o600))
	absOutside, err := filepath.Abs(outside)
	require.NoError(t, err)
	require.NoError(t, os.Symlink(absOutside, filepath.Join("uploads", "media", "out.webp")))
	thumb := "thumb.webp"
	require.NoError(t, os.Symlink(filepath.Join("..", ".private-staging", "abc", "input"), filepath.Join("uploads", "media", thumb)))

	link := &models.MediaAttachment{
		AccountId: 1, FileName: "link.webp", FilePath: "link.webp", ContentType: "image/webp",
		Type: models.MediaTypeImage, ProcessState: models.ProcessReady, FileSize: int64(len(secret)),
		ThumbnailPath: &thumb,
	}
	require.NoError(t, gdb.Create(link).Error)
	poisonName := "evil.webp"
	poison := &models.MediaAttachment{
		AccountId: 1, FileName: poisonName, FilePath: "uploads/.private-staging/abc/input", ContentType: "image/webp",
		Type: models.MediaTypeImage, ProcessState: models.ProcessReady, FileSize: int64(len(secret)),
	}
	require.NoError(t, gdb.Create(poison).Error)
	alias := &models.MediaAttachment{
		AccountId: 1, FileName: "input", FilePath: "aliasdir/input", ContentType: "image/webp",
		Type: models.MediaTypeImage, ProcessState: models.ProcessReady, FileSize: int64(len(secret)),
	}
	require.NoError(t, gdb.Create(alias).Error)
	escaped := &models.MediaAttachment{
		AccountId: 1, FileName: "out.webp", FilePath: "out.webp", ContentType: "image/webp",
		Type: models.MediaTypeImage, ProcessState: models.ProcessReady, FileSize: int64(len(secret)),
	}
	require.NoError(t, gdb.Create(escaped).Error)

	rc, _, err := storage.Default().Open(t.Context(), ".private-staging/abc/input")
	require.Error(t, err)
	if rc != nil {
		_ = rc.Close()
	}
	rc, _, err = storage.Default().Open(t.Context(), "media/link.webp")
	require.Error(t, err)
	if rc != nil {
		_ = rc.Close()
	}
	var listed []string
	require.NoError(t, storage.Default().List(t.Context(), "", func(info storage.ObjectInfo) error {
		listed = append(listed, info.Key)
		return nil
	}))
	for _, key := range listed {
		require.NotContains(t, key, ".private-staging")
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/media/:id/*file", Serve)
	router.GET("/emoji-assets/:file", emojiasset.Serve)
	router.GET("/card-images/:file", linkcard.ServeImage)
	id := strconv.FormatUint(link.Id, 10)
	targets := []string{
		"/uploads/.private-staging/abc/input",
		"/media/" + id + "/../../.private-staging/abc/input",
		"/media/" + id + "/%2e%2e/.private-staging/abc/input",
		"/media/" + id + "/link.webp",
		"/media/" + id + "/thumbnail/" + thumb,
		"/media/" + strconv.FormatUint(poison.Id, 10) + "/" + poisonName,
		"/media/" + strconv.FormatUint(alias.Id, 10) + "/input",
		"/media/" + strconv.FormatUint(escaped.Id, 10) + "/out.webp",
		"/emoji-assets/.private-staging/abc/input",
		"/emoji-assets/" + id,
		"/card-images/.private-staging/abc/input",
		"/card-images/" + strings.Repeat("a", 32) + ".webp",
	}
	for _, target := range targets {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if bytes.Contains(w.Body.Bytes(), secret) {
			t.Fatalf("%s returned staged bytes (%d)", target, w.Code)
		}
	}
	for _, target := range []string{
		"/media/" + id + "/link.webp",
		"/media/" + id + "/thumbnail/" + thumb,
		"/media/" + strconv.FormatUint(alias.Id, 10) + "/input",
		"/media/" + strconv.FormatUint(escaped.Id, 10) + "/out.webp",
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		require.Equal(t, http.StatusNotFound, w.Code, target)
	}
}
