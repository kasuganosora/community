package media

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServe(t *testing.T) {
	withTempWorkDir(t)
	gdb := setupMediaTestDB(t)
	gin.SetMode(gin.TestMode)

	dayDir := filepath.Join("uploads", "media", "2026", "09", "26")
	require.NoError(t, os.MkdirAll(dayDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dayDir, "a.png"), validPNGBytes(), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dayDir, "thumb_a.png"), validPNGBytes(), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join("uploads", "secret.txt"), []byte("secret"), 0o600))

	thumb := "thumb_a.png"
	m := &models.MediaAttachment{
		AccountId: 1, FileName: "a.png", FilePath: "2026/09/26/a.png",
		ContentType: "image/png", Type: models.MediaTypeImage, ThumbnailPath: &thumb,
	}
	require.NoError(t, gdb.Create(m).Error)

	r := gin.New()
	r.GET("/media/:id/*file", Serve)

	get := func(p string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))

		return w
	}

	w := get("/media/1/a.png")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, validPNGBytes(), w.Body.Bytes())

	w = get("/media/1/thumbnail/thumb_a.png")
	assert.Equal(t, http.StatusOK, w.Code)

	for _, p := range []string{
		"/media/1/other.png",
		"/media/1/../../secret.txt",
		"/media/1/thumbnail/../../../secret.txt",
		"/media/2/a.png",
	} {
		assert.Equal(t, http.StatusNotFound, get(p).Code, p)
	}
}

func TestGet_OnlyOwnerCanQuery(t *testing.T) {
	gdb := setupMediaTestDB(t)
	require.NoError(t, gdb.Create(&models.MediaAttachment{
		AccountId: 99, FileName: "x.png", FilePath: "x.png", ContentType: "image/png",
	}).Error)

	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: "1"}}, httptest.NewRequest(http.MethodGet, "/", nil))
	Get(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
