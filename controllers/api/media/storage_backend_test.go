package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// objectStores returns the object-storage backends the S3-mode tests run
// against: always an in-process fake, plus a real S3 server (MinIO,
// versitygw, ...) when TEST_S3_* is configured.
func objectStores(t *testing.T) map[string]func(t *testing.T) storage.Backend {
	return map[string]func(t *testing.T) storage.Backend{
		"fake": func(*testing.T) storage.Backend { return storagetest.NewMemory() },
		"s3": func(t *testing.T) storage.Backend {
			env := storagetest.S3FromEnv(t)
			return storage.NewS3Backend(env.Config(env.NewBucket(t)))
		},
	}
}

func assertNoLocalFiles(t *testing.T, dir string) {
	t.Helper()
	_ = filepath.Walk(filepath.Join(dir, "uploads"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			t.Errorf("S3 mode must not keep a local copy: %s", p)
		}
		return nil
	})
}

func serveRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/media/:id/*file", Serve)
	return r
}

func doGet(r *gin.Engine, p string, hdr map[string]string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, p, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	r.ServeHTTP(w, req)
	return w
}

func requireObject(t *testing.T, b storage.Backend, key string, want []byte) {
	t.Helper()
	rc, _, err := b.Open(context.Background(), key)
	require.NoError(t, err, key)
	var got bytes.Buffer
	_, _ = got.ReadFrom(rc)
	_ = rc.Close()
	if want != nil {
		assert.Equal(t, want, got.Bytes(), key)
	}
}

func requireNoObject(t *testing.T, b storage.Backend, key string) {
	t.Helper()
	_, err := b.Stat(context.Background(), key)
	require.ErrorIs(t, err, storage.ErrNotFound, key)
}

func TestObjectStorage_AllUploadPaths(t *testing.T) {
	for name, mk := range objectStores(t) {
		t.Run(name, func(t *testing.T) {
			dir := withTempWorkDir(t)
			gdb := setupMediaTestDB(t)
			b := mk(t)
			defer storage.SetDefault(b)()
			r := serveRouter()
			png := validPNGBytes()
			mp3 := []byte{0x49, 0x44, 0x33, 0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xFF, 0xFB, 0x90, 0x00, 1, 2, 3, 4}

			// --- /api/v1/media (the web UI path) -----------------------------
			c, w := newHandlerContext(t, nil, createMediaWithMultipartRequest(t, "pic.png", "image/png", png, nil))
			Upload(c)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var resp struct {
				ID         string `json:"id"`
				URL        string `json:"url"`
				PreviewURL string `json:"preview_url"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

			var v1 models.MediaAttachment
			require.NoError(t, gdb.First(&v1, resp.ID).Error)
			assert.NotContains(t, v1.FilePath, "://", "rows keep keys, not bucket URLs")
			assert.False(t, strings.HasPrefix(v1.FilePath, "uploads"))
			requireObject(t, b, "media/"+v1.FilePath, png)
			require.NotNil(t, v1.ThumbnailPath)
			requireObject(t, b, v1.ThumbnailStorageKey(), nil)
			assert.NotEmpty(t, v1.Blurhash)
			assert.Contains(t, resp.URL, "/media/"+resp.ID+"/"+v1.FileName)
			assert.Contains(t, resp.PreviewURL, "/media/"+resp.ID+"/thumbnail/"+*v1.ThumbnailPath)

			g := doGet(r, "/media/"+resp.ID+"/"+v1.FileName, nil)
			require.Equal(t, http.StatusOK, g.Code)
			assert.Equal(t, "image/png", g.Header().Get("Content-Type"))
			assert.Equal(t, "nosniff", g.Header().Get("X-Content-Type-Options"))
			assert.Equal(t, png, g.Body.Bytes())

			g = doGet(r, "/media/"+resp.ID+"/thumbnail/"+*v1.ThumbnailPath, nil)
			require.Equal(t, http.StatusOK, g.Code)
			assert.Equal(t, "image/png", g.Header().Get("Content-Type"))

			// --- /api/v2/media (async) ---------------------------------------
			c, w = newHandlerContext(t, nil, createMediaWithMultipartRequest(t, "clip.mp3", "audio/mpeg", mp3, nil))
			UploadAsync(c)
			require.Equal(t, http.StatusAccepted, w.Code, w.Body.String())
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			var v2 models.MediaAttachment
			require.Eventually(t, func() bool {
				return gdb.First(&v2, resp.ID).Error == nil && v2.FilePath != ""
			}, 10*time.Second, 20*time.Millisecond)
			requireObject(t, b, v2.StorageKey(), mp3)
			g = doGet(r, "/media/"+resp.ID+"/"+v2.FileName, nil)
			require.Equal(t, http.StatusOK, g.Code)
			assert.Equal(t, "audio/mpeg", g.Header().Get("Content-Type"))
			assert.Equal(t, mp3, g.Body.Bytes())
			g = doGet(r, "/media/"+resp.ID+"/"+v2.FileName, map[string]string{"Range": "bytes=0-3"})
			require.Equal(t, http.StatusPartialContent, g.Code)
			assert.Equal(t, mp3[:4], g.Body.Bytes())

			// --- legacy multipart profile image (PUT /accounts/me) -----------
			fh := fileHeader(t, "avatar.png", "application/octet-stream", png)
			avatarURL, err := StoreAccountImage(context.Background(), 1, fh)
			require.NoError(t, err)
			assert.True(t, strings.HasPrefix(avatarURL, config.C.AppBaseURL+"/media/"))
			avatarPath := strings.TrimPrefix(avatarURL, config.C.AppBaseURL)
			g = doGet(r, avatarPath, nil)
			require.Equal(t, http.StatusOK, g.Code)
			assert.Equal(t, png, g.Body.Bytes())
			_, err = StoreAccountImage(context.Background(), 1, fileHeader(t, "x.txt", "image/png", []byte("not an image at all")))
			require.Error(t, err)

			// Nothing was written to the local disk in S3 mode.
			assertNoLocalFiles(t, dir)

			// --- delete -------------------------------------------------------
			c, w = newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(v1.Id, 10)}},
				httptest.NewRequest(http.MethodDelete, "/api/v1/media/"+strconv.FormatUint(v1.Id, 10), nil))
			Delete(c)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			requireNoObject(t, b, v1.StorageKey())
			requireNoObject(t, b, v1.ThumbnailStorageKey())
			assert.Equal(t, http.StatusNotFound, doGet(r, "/media/"+strconv.FormatUint(v1.Id, 10)+"/"+v1.FileName, nil).Code)

			// --- rows written by the buggy release (full bucket URL) ---------
			require.NoError(t, b.Put(context.Background(), "media/2026/10/08/legacy.png", bytes.NewReader(png), int64(len(png)), "image/png"))
			legacy := &models.MediaAttachment{AccountId: 1, FileName: "legacy.png", FilePath: "https://acct.r2.cloudflarestorage.com/community-file/media/2026/10/08/legacy.png", ContentType: "image/png", Type: models.MediaTypeImage}
			require.NoError(t, gdb.Create(legacy).Error)
			g = doGet(r, "/media/"+strconv.FormatUint(legacy.Id, 10)+"/legacy.png", nil)
			require.Equal(t, http.StatusOK, g.Code)
			assert.Equal(t, png, g.Body.Bytes())
			require.NoError(t, legacy.DeleteStoredFiles(context.Background()))
			requireNoObject(t, b, "media/2026/10/08/legacy.png")
		})
	}
}

func TestObjectStorage_PublicURLRedirectAndFailures(t *testing.T) {
	withTempWorkDir(t)
	gdb := setupMediaTestDB(t)
	mem := storagetest.NewMemory()
	mem.PublicBase = "https://media.example.com"
	defer storage.SetDefault(mem)()
	r := serveRouter()

	png := validPNGBytes()
	require.NoError(t, mem.Put(context.Background(), "media/2026/10/09/a.png", bytes.NewReader(png), int64(len(png)), "image/png"))
	require.NoError(t, mem.Put(context.Background(), "media/2026/10/09/a.html", strings.NewReader("<script>"), 8, "text/html"))
	thumb := "thumb_a.png"
	img := &models.MediaAttachment{AccountId: 1, FileName: "a.png", FilePath: "2026/10/09/a.png", ContentType: "image/png", Type: models.MediaTypeImage, ThumbnailPath: &thumb}
	html := &models.MediaAttachment{AccountId: 1, FileName: "a.html", FilePath: "2026/10/09/a.html", ContentType: "text/html"}
	require.NoError(t, gdb.Create(img).Error)
	require.NoError(t, gdb.Create(html).Error)

	g := doGet(r, "/media/1/a.png", nil)
	require.Equal(t, http.StatusFound, g.Code)
	assert.Equal(t, "https://media.example.com/media/2026/10/09/a.png", g.Header().Get("Location"))
	g = doGet(r, "/media/1/thumbnail/thumb_a.png", nil)
	require.Equal(t, http.StatusFound, g.Code)
	assert.Equal(t, "https://media.example.com/media/2026/10/09/thumb_a.png", g.Header().Get("Location"))

	// Non-inline types are never handed to the bucket; the app streams them
	// as attachments.
	g = doGet(r, "/media/2/a.html", nil)
	require.Equal(t, http.StatusOK, g.Code)
	assert.Contains(t, g.Header().Get("Content-Disposition"), "attachment")

	// Backend outage: 503, not a misleading 404.
	mem.PublicBase = ""
	mem.Fail = errors.New("boom")
	assert.Equal(t, http.StatusServiceUnavailable, doGet(r, "/media/1/a.png", nil).Code)

	// Upload fails cleanly when the bucket rejects writes.
	c, w := newHandlerContext(t, nil, createMediaWithMultipartRequest(t, "pic.png", "image/png", png, nil))
	Upload(c)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var n int64
	require.NoError(t, gdb.Model(&models.MediaAttachment{}).Count(&n).Error)
	assert.Equal(t, int64(2), n)
}

func fileHeader(t *testing.T, name, contentType string, data []byte) *multipart.FileHeader {
	t.Helper()
	req := createMediaWithMultipartRequest(t, name, contentType, data, nil)
	require.NoError(t, req.ParseMultipartForm(32<<20))
	return req.MultipartForm.File["file"][0]
}
