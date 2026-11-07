package media

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupMediaTestDB opens an in-memory SQLite DB, auto-migrates the
// MediaAttachment model, and injects it via database.SetTestDB so that
// db.GetDB() returns it inside the controllers. The previous global state is
// restored on cleanup.
func setupMediaTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gdb, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{
		SkipDefaultTransaction:                   true,
		IgnoreRelationshipsWhenMigrating:         true,
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)

	// Every new connection to ":memory:" opens a separate, empty database.
	// UploadAsync's background goroutine and the test's polling query run
	// concurrently, so with a pool the goroutine could get a fresh
	// connection and fail with "no such table: media_attachments". Pin the
	// pool to a single connection so everyone sees the same database.
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	require.NoError(t, gdb.AutoMigrate(&models.MediaAttachment{}, &models.MediaProcessTask{}))

	prev := database.GetDB()
	database.SetTestDB(gdb)
	t.Cleanup(func() {
		if prev != nil {
			database.SetTestDB(prev)
		} else {
			database.ResetTestDB()
		}
	})

	return gdb
}

// newHandlerContext builds a gin.Context with an authenticated account and the
// given path parameters. The context writes to the returned recorder.
func newHandlerContext(t *testing.T, params gin.Params, req *http.Request) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = params
	if req != nil {
		c.Request = req
	}
	c.Set("authenticated", true)
	c.Set("account", createTestAccount())
	return c, w
}

// createMediaWithMultipartRequest builds a multipart/form-data request that
// also includes form fields (description/focus) alongside the file part. The
// file part's Content-Type header is set to the provided value so the upload
// controller's declared-type validation can be exercised.
func createMediaWithMultipartRequest(t *testing.T, fileName, contentType string, fileContent []byte, fields map[string]string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for k, v := range fields {
		require.NoError(t, writer.WriteField(k, v))
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+fileName+`"`)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(h)
	require.NoError(t, err)
	_, err = part.Write(fileContent)
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", body) //nolint:noctx
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

// newJSONRequest builds a JSON body request for Update tests.
func newJSONRequest(t *testing.T, path, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body)) //nolint:noctx
	req.Header.Set("Content-Type", "application/json")
	return req
}

// withTempWorkDir sets the working directory to a temp dir (so uploads/ is
// created there and cleaned up automatically), restoring the original dir on
// cleanup. Returns the temp dir path.
func withTempWorkDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig, err := filepath.Abs(".")
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir
}

// validPNGBytes returns a minimal valid PNG image (4x4 transparent) suitable
// for exercising the magic-byte validation and the image processor path.
func validPNGBytes() []byte {
	// Generate a valid 1×1 RGBA PNG using the standard library.
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// validWAV is a short PCM file ffmpeg can decode. A header-only MP3 is not.
func validWAV() []byte {
	payload := bytes.Repeat([]byte{128}, 32)
	buf := make([]byte, 44+len(payload))
	copy(buf, "RIFF")
	binary.LittleEndian.PutUint32(buf[4:], uint32(36+len(payload)))
	copy(buf[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(buf[16:], 16)
	binary.LittleEndian.PutUint16(buf[20:], 1)
	binary.LittleEndian.PutUint16(buf[22:], 1)
	binary.LittleEndian.PutUint32(buf[24:], 8000)
	binary.LittleEndian.PutUint32(buf[28:], 8000)
	binary.LittleEndian.PutUint16(buf[32:], 1)
	binary.LittleEndian.PutUint16(buf[34:], 8)
	copy(buf[36:], "data")
	binary.LittleEndian.PutUint32(buf[40:], uint32(len(payload)))
	copy(buf[44:], payload)
	return buf
}

// ---------------------------------------------------------------------------
// Get handler
// ---------------------------------------------------------------------------

func TestGet_NotFound(t *testing.T) {
	setupMediaTestDB(t)

	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: "999"}}, httptest.NewRequest(http.MethodGet, "/api/v1/media/999", nil))
	Get(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "Not found")
}

func TestGetProcessingOwnerOnly(t *testing.T) {
	gdb := setupMediaTestDB(t)
	row := &models.MediaAttachment{AccountId: 1, FileName: "processing", FilePath: "", ContentType: "video/mp4", Type: models.MediaTypeVideo, ProcessState: models.ProcessProcessing, ProcessProgress: 40}
	require.NoError(t, gdb.Create(row).Error)
	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(row.Id, 10)}}, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+strconv.FormatUint(row.Id, 10)+"/processing", nil))
	GetProcessing(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"progress":40`)
	c, w = newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(row.Id, 10)}}, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+strconv.FormatUint(row.Id, 10)+"/processing", nil))
	c.Set("account", &models.Account{Id: 2})
	GetProcessing(c)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetReportsInProgressAndFailureWithoutAPublicFile(t *testing.T) {
	gdb := setupMediaTestDB(t)
	for _, state := range []string{models.ProcessQueued, models.ProcessProcessing, models.ProcessSaving} {
		require.NoError(t, gdb.Exec("DELETE FROM media_attachments").Error)
		row := &models.MediaAttachment{AccountId: 1, FileName: "secret.png", FilePath: "private/secret.png", ContentType: "image/png", Type: models.MediaTypeImage, ProcessState: state}
		require.NoError(t, gdb.Create(row).Error)
		c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(row.Id, 10)}}, httptest.NewRequest(http.MethodGet, "/", nil))
		Get(c)
		require.Equal(t, http.StatusPartialContent, w.Code, state)
		require.NotContains(t, w.Body.String(), "private/secret.png")
	}
	for _, state := range []string{models.ProcessFailed, models.ProcessCancelled} {
		require.NoError(t, gdb.Exec("DELETE FROM media_attachments").Error)
		row := &models.MediaAttachment{AccountId: 1, FileName: "secret.png", FilePath: "private/secret.png", ContentType: "image/png", Type: models.MediaTypeImage, ProcessState: state, ProcessError: "media processing failed"}
		require.NoError(t, gdb.Create(row).Error)
		c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(row.Id, 10)}}, httptest.NewRequest(http.MethodGet, "/", nil))
		Get(c)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, state)
		require.NotContains(t, w.Body.String(), "private/secret.png")
	}
}

func TestUpdateDoesNotOverwriteProcessState(t *testing.T) {
	gdb := setupMediaTestDB(t)
	row := &models.MediaAttachment{AccountId: 1, FileName: "a.png", FilePath: "a.png", ContentType: "image/png", Type: models.MediaTypeImage, ProcessState: models.ProcessProcessing, ProcessProgress: 40}
	require.NoError(t, gdb.Create(row).Error)
	require.NoError(t, gdb.Callback().Update().Before("gorm:update").Register("worker-advanced", func(tx *gorm.DB) {
		if tx.Statement.Table != "media_attachments" {
			return
		}
		_, _ = tx.Statement.ConnPool.ExecContext(tx.Statement.Context, "UPDATE media_attachments SET process_state = ?, process_progress = ? WHERE id = ?", models.ProcessSaving, 90, row.Id)
	}))
	req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"description":"caption"}`))
	req.Header.Set("Content-Type", "application/json")
	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(row.Id, 10)}}, req)
	Update(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var got models.MediaAttachment
	require.NoError(t, gdb.First(&got, row.Id).Error)
	require.Equal(t, models.ProcessSaving, got.ProcessState)
	require.Equal(t, 90, got.ProcessProgress)
	require.Equal(t, "caption", got.Description)
}

func TestGet_Success(t *testing.T) {
	gdb := setupMediaTestDB(t)

	media := &models.MediaAttachment{
		AccountId:   1,
		FileName:    "test.png",
		FilePath:    "2024/01/01/test.png",
		FileSize:    1024,
		ContentType: "image/png",
		Type:        models.MediaTypeImage,
	}
	require.NoError(t, gdb.Create(media).Error)

	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(media.Id, 10)}},
		httptest.NewRequest(http.MethodGet, "/api/v1/media/"+strconv.FormatUint(media.Id, 10), nil))
	Get(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "test.png")
	assert.Contains(t, w.Body.String(), "image")
}

// ---------------------------------------------------------------------------
// Update handler
// ---------------------------------------------------------------------------

func TestUpdate_NotFound(t *testing.T) {
	setupMediaTestDB(t)

	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: "999"}},
		newJSONRequest(t, "/api/v1/media/999", `{"description":"x"}`))
	Update(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdate_InvalidJSON(t *testing.T) {
	gdb := setupMediaTestDB(t)
	media := &models.MediaAttachment{AccountId: 1, FileName: "f", FilePath: "p", ContentType: "image/png", Type: models.MediaTypeImage}
	require.NoError(t, gdb.Create(media).Error)

	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(media.Id, 10)}},
		newJSONRequest(t, "/api/v1/media/"+strconv.FormatUint(media.Id, 10), `{bad json`))
	Update(c)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdate_SuccessDescription(t *testing.T) {
	gdb := setupMediaTestDB(t)
	media := &models.MediaAttachment{AccountId: 1, FileName: "f", FilePath: "p", ContentType: "image/png", Type: models.MediaTypeImage}
	require.NoError(t, gdb.Create(media).Error)

	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(media.Id, 10)}},
		newJSONRequest(t, "/api/v1/media/"+strconv.FormatUint(media.Id, 10), `{"description":"A cute cat"}`))
	Update(c)

	require.Equal(t, http.StatusOK, w.Code)

	var got models.MediaAttachment
	require.NoError(t, gdb.First(&got, media.Id).Error)
	assert.Equal(t, "A cute cat", got.Description)
}

func TestUpdate_FocusClampedOutOfRange(t *testing.T) {
	gdb := setupMediaTestDB(t)
	media := &models.MediaAttachment{AccountId: 1, FileName: "f", FilePath: "p", ContentType: "image/png", Type: models.MediaTypeImage}
	require.NoError(t, gdb.Create(media).Error)

	// Focus out of [-1,1] range should be ignored (not set).
	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(media.Id, 10)}},
		newJSONRequest(t, "/api/v1/media/"+strconv.FormatUint(media.Id, 10), `{"focus":"5,-3"}`))
	Update(c)

	require.Equal(t, http.StatusOK, w.Code)

	var got models.MediaAttachment
	require.NoError(t, gdb.First(&got, media.Id).Error)
	assert.InDelta(t, 0.0, got.FocusX, 0.0001)
	assert.InDelta(t, 0.0, got.FocusY, 0.0001)
}

func TestUpdate_FocusValid(t *testing.T) {
	gdb := setupMediaTestDB(t)
	media := &models.MediaAttachment{AccountId: 1, FileName: "f", FilePath: "p", ContentType: "image/png", Type: models.MediaTypeImage}
	require.NoError(t, gdb.Create(media).Error)

	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(media.Id, 10)}},
		newJSONRequest(t, "/api/v1/media/"+strconv.FormatUint(media.Id, 10), `{"focus":"0.5,-0.3"}`))
	Update(c)

	require.Equal(t, http.StatusOK, w.Code)

	var got models.MediaAttachment
	require.NoError(t, gdb.First(&got, media.Id).Error)
	assert.InDelta(t, 0.5, got.FocusX, 0.0001)
	assert.InDelta(t, -0.3, got.FocusY, 0.0001)
}

func TestUpdate_WrongAccount(t *testing.T) {
	gdb := setupMediaTestDB(t)
	// Media owned by account 2, but the test context uses account id=1.
	media := &models.MediaAttachment{AccountId: 2, FileName: "f", FilePath: "p", ContentType: "image/png", Type: models.MediaTypeImage}
	require.NoError(t, gdb.Create(media).Error)

	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(media.Id, 10)}},
		newJSONRequest(t, "/api/v1/media/"+strconv.FormatUint(media.Id, 10), `{"description":"x"}`))
	Update(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// ---------------------------------------------------------------------------
// Delete handler
// ---------------------------------------------------------------------------

func TestDelete_NotFound(t *testing.T) {
	setupMediaTestDB(t)

	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: "999"}}, httptest.NewRequest(http.MethodDelete, "/api/v1/media/999", nil))
	Delete(c)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDelete_AttachedToStatus(t *testing.T) {
	gdb := setupMediaTestDB(t)
	statusId := uint64(42)
	media := &models.MediaAttachment{AccountId: 1, FileName: "f", FilePath: "p", ContentType: "image/png", Type: models.MediaTypeImage, StatusId: &statusId}
	require.NoError(t, gdb.Create(media).Error)

	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(media.Id, 10)}}, httptest.NewRequest(http.MethodDelete, "/api/v1/media/"+strconv.FormatUint(media.Id, 10), nil))
	Delete(c)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	assert.Contains(t, w.Body.String(), "attached to a status")

	// Record should still exist.
	var got models.MediaAttachment
	require.NoError(t, gdb.First(&got, media.Id).Error)
}

func TestDelete_Success(t *testing.T) {
	gdb := setupMediaTestDB(t)
	media := &models.MediaAttachment{AccountId: 1, FileName: "f", FilePath: "p", ContentType: "image/png", Type: models.MediaTypeImage}
	require.NoError(t, gdb.Create(media).Error)

	c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(media.Id, 10)}}, httptest.NewRequest(http.MethodDelete, "/api/v1/media/"+strconv.FormatUint(media.Id, 10), nil))
	Delete(c)

	assert.Equal(t, http.StatusOK, w.Code)

	// Record should be gone.
	var got models.MediaAttachment
	err := gdb.First(&got, media.Id).Error
	assert.Error(t, err) // record not found
}
