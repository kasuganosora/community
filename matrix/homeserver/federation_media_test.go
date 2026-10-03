package homeserver

import (
	"bytes"
	"fmt"
	"github.com/kasuganosora/community/db"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFederationMediaMultipartPreservesOriginalBytesAndFilename(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	original := []byte{0, 255, 3, 10, 13, 0, 7}
	writeFederationMedia(ctx, bytes.NewReader(original), "application/octet-stream", "附件 \"test\".bin")
	parsed, err := parseFederationMedia(recorder.Header().Get("Content-Type"), recorder.Body.Bytes())
	require.NoError(t, err)
	require.Equal(t, original, parsed.Data)
	require.Equal(t, "application/octet-stream", parsed.ContentType)
	require.Equal(t, "附件 \"test\".bin", parsed.Filename)
}
func TestFederationMediaRejectsInvalidRemoteResponses(t *testing.T) {
	for _, typ := range []string{"application/json", "text/html", "multipart/mixed"} {
		_, err := parseFederationMedia(typ, []byte(`{"errcode":"M_NOT_FOUND"}`))
		require.Error(t, err)
	}
	_, err := parseFederationMedia("multipart/mixed; boundary=qa", []byte("--qa\r\nContent-Type: application/json\r\n\r\n{}\r\n--qa\r\nLocation: http://127.0.0.1/private\r\n\r\n\r\n--qa--\r\n"))
	require.ErrorContains(t, err, "redirect")
}
func TestOriginalMediaFilenameIsNotStorageNameOrPath(t *testing.T) {
	require.Equal(t, "report.txt", originalMediaFilename(&multipart.FileHeader{Filename: `C:\fakepath\report.txt`}, "random.txt"))
	require.Equal(t, "picture.png", originalMediaFilename(&multipart.FileHeader{Filename: "/tmp/picture.png"}, "random.png"))
	require.Equal(t, "random", originalMediaFilename(nil, "random"))
}

// An independent literal wire fixture keeps protocol tests independent from the
// production multipart writer and checks the signed authenticated endpoints.
func writeMockFederationMedia(w http.ResponseWriter, typ string, body []byte) {
	w.Header().Set("Content-Type", "multipart/mixed; boundary=fixture-boundary")
	_, _ = fmt.Fprintf(w, "--fixture-boundary\r\nContent-Type: application/json\r\n\r\n{}\r\n--fixture-boundary\r\nContent-Type: %s\r\nContent-Disposition: inline; filename=test.bin\r\n\r\n", typ)
	_, _ = w.Write(body)
	_, _ = w.Write([]byte("\r\n--fixture-boundary--\r\n"))
}

func TestThumbnailLegacyBasenameResolvedBesideOriginalMedia(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&MatrixMedia{}, &MatrixThumbnail{}))
	root := t.TempDir()
	content := []byte("thumbnail exact bytes")
	require.NoError(t, os.WriteFile(filepath.Join(root, "thumb_image.png"), content, 0600))
	require.NoError(t, db.GetDB().Create(&MatrixMedia{MediaID: "legacy-thumb", ServerName: "local.test", FilePath: filepath.Join(root, "image.png")}).Error)
	require.NoError(t, db.GetDB().Create(&MatrixThumbnail{MediaID: "legacy-thumb", FilePath: "thumb_image.png"}).Error)
	service := NewMediaService(&HomeServer{Config: &HSConfig{ServerName: "local.test"}})
	_, reader, err := service.GetThumbnail("local.test", "legacy-thumb", 64, 64, "scale")
	require.NoError(t, err)
	defer reader.Close()
	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, content, actual)
	_, _, err = service.GetThumbnail("other.test", "legacy-thumb", 64, 64, "scale")
	require.Error(t, err)
}
