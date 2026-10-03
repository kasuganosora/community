package media

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServeUntrustedActiveFormatsAlwaysDownload(t *testing.T) {
	withTempWorkDir(t)
	conn := setupMediaTestDB(t)
	require.NoError(t, os.MkdirAll(filepath.Join("uploads", "media"), 0700))
	router := gin.New()
	router.GET("/media/:id/*file", Serve)
	for n, tc := range []struct {
		mime   string
		inline bool
	}{
		{"text/html", false}, {"image/svg+xml", false}, {"application/javascript", false}, {"text/javascript", false}, {"application/octet-stream", false}, {"application/pdf", false}, {"invalid;", false},
		{"image/png", true}, {"image/jpeg", true}, {"image/gif", true}, {"image/webp", true}, {"image/avif", true}, {"audio/mpeg", true}, {"audio/ogg", true}, {"video/mp4", true}, {"video/webm", true},
	} {
		t.Run(tc.mime, func(t *testing.T) {
			name := fmt.Sprintf("%d.html", n)
			payload := []byte(`<html><script>alert(document.domain)</script></html>`)
			require.NoError(t, os.WriteFile(filepath.Join("uploads", "media", name), payload, 0600))
			item := models.MediaAttachment{AccountId: 1, FileName: name, FilePath: name, ContentType: tc.mime}
			require.NoError(t, conn.Create(&item).Error)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/media/%d/%s", item.Id, name), nil))
			require.Equal(t, 200, w.Code)
			require.Equal(t, payload, w.Body.Bytes())
			require.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
			require.Equal(t, !tc.inline, strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment"))
			if tc.inline {
				require.Equal(t, tc.mime, w.Header().Get("Content-Type"), "inert MIME must not be extension-sniffed as HTML")
			}
		})
	}
	// Imported thumbnail filenames cannot opt HTML into inline rendering.
	name := "unsafe.svg"
	require.NoError(t, os.WriteFile(filepath.Join("uploads", "media", name), []byte(`<svg onload="alert(1)"></svg>`), 0600))
	item := models.MediaAttachment{AccountId: 1, FileName: "original.bin", FilePath: "original.bin", ThumbnailPath: &name}
	require.NoError(t, conn.Create(&item).Error)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("/media/%d/thumbnail/%s", item.Id, name), nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
}
