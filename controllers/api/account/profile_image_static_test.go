package account

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/cache"
	mediactl "github.com/kasuganosora/community/controllers/api/media"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/mediaproc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUpdateCredentialsPersistsOwnedStaticImages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{
		SkipDefaultTransaction:                   true,
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(
		&models.Account{}, &models.AccountExtra{}, &models.Emoji{},
		&models.MediaAttachment{}, &models.MediaProcessTask{}, &models.MediaQueueGate{},
	))
	previousDB := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previousDB) })

	useAccountCache(t)

	const (
		oldAvatar       = "http://localhost:8080/media/9/previous-avatar.png"
		oldStatic       = "http://localhost:8080/media/9/previous-static.png"
		oldHeader       = "http://localhost:8080/media/9/previous-header.png"
		oldHeaderStatic = "http://localhost:8080/media/9/previous-header-static.png"
	)
	owner := &models.Account{
		Id: 42, Username: "owner", DisplayName: "before", Note: "kept",
		Avatar: oldAvatar, AvatarStatic: oldStatic, Header: oldHeader, HeaderStatic: oldHeaderStatic,
	}
	other := &models.Account{Id: 43, Username: "other"}
	require.NoError(t, conn.Create(owner).Error)
	require.NoError(t, conn.Create(other).Error)

	stillA := uploadProfileImage(t, owner, pngBytes(t, color.RGBA{R: 20, G: 40, B: 60, A: 255}), "still-a.png", "image/png")
	stillB := uploadProfileImage(t, owner, pngBytes(t, color.RGBA{R: 200, G: 10, B: 10, A: 255}), "still-b.png", "image/png")
	header := uploadProfileImage(t, owner, pngBytes(t, color.RGBA{R: 10, G: 180, B: 30, A: 255}), "header.png", "image/png")
	animatedMedia := storeProcessedAnimation(t, conn, owner)
	foreign := uploadProfileImage(t, other, pngBytes(t, color.RGBA{R: 1, G: 2, B: 3, A: 255}), "foreign.png", "image/png")

	cacheKey := fmt.Sprintf(cache.KeyAccountByAcct, fmt.Sprint(owner.Id))
	require.NoError(t, cache.GetCache().Set(context.Background(), cacheKey, map[string]any{
		"avatar": oldAvatar, "avatar_static": oldStatic,
	}, cache.DefaultExpiration))

	body := patchCredentials(t, owner, map[string]any{"display_name": "after", "avatar": stillA.URL})
	require.Equal(t, stillA.URL, body["avatar"])
	require.Equal(t, stillA.URL, body["avatar_static"])
	require.Equal(t, oldHeader, body["header"])
	saved := reloadAccount(t, conn, owner.Id)
	require.Equal(t, "after", saved.DisplayName)
	require.Equal(t, "kept", saved.Note)
	require.Equal(t, stillA.URL, saved.Avatar)
	require.Equal(t, stillA.URL, saved.AvatarStatic)
	require.Equal(t, oldHeader, saved.Header)
	require.Equal(t, oldHeaderStatic, saved.HeaderStatic)
	require.NotEqual(t, oldStatic, saved.AvatarStatic)
	var stale map[string]any
	require.ErrorIs(t, cache.GetCache().Get(context.Background(), cacheKey, &stale), cache.ErrCacheMiss)

	require.NoError(t, cache.GetCache().Set(context.Background(), cacheKey, map[string]any{
		"avatar": oldAvatar, "avatar_static": oldStatic, "header": oldHeader, "header_static": oldHeaderStatic,
	}, cache.DefaultExpiration))
	patchCredentials(t, owner, map[string]any{"note": "renamed only"})
	saved = reloadAccount(t, conn, owner.Id)
	require.Equal(t, "renamed only", saved.Note)
	require.Equal(t, stillA.URL, saved.Avatar)
	require.Equal(t, stillA.URL, saved.AvatarStatic)
	require.Equal(t, oldHeader, saved.Header)
	require.Equal(t, oldHeaderStatic, saved.HeaderStatic)
	require.ErrorIs(t, cache.GetCache().Get(context.Background(), cacheKey, &stale), cache.ErrCacheMiss)

	body = patchCredentials(t, owner, map[string]any{"avatar": stillB.URL, "header": header.URL})
	require.Equal(t, stillB.URL, body["avatar"])
	require.Equal(t, stillB.URL, body["avatar_static"])
	require.Equal(t, header.URL, body["header"])
	require.Equal(t, header.URL, body["header_static"])
	saved = reloadAccount(t, conn, owner.Id)
	require.Equal(t, stillB.URL, saved.Avatar)
	require.Equal(t, stillB.URL, saved.AvatarStatic)
	require.Equal(t, header.URL, saved.Header)
	require.Equal(t, header.URL, saved.HeaderStatic)
	require.Equal(t, "renamed only", saved.Note)

	body = patchCredentials(t, owner, map[string]any{"avatar": stillB.URL})
	require.Equal(t, stillB.URL, body["avatar_static"])
	body = patchCredentials(t, owner, map[string]any{"avatar": stillA.URL})
	require.Equal(t, stillA.URL, body["avatar"])
	require.Equal(t, stillA.URL, body["avatar_static"])
	saved = reloadAccount(t, conn, owner.Id)
	require.Equal(t, header.URL, saved.Header)
	require.Equal(t, header.URL, saved.HeaderStatic)

	body = patchCredentials(t, owner, map[string]any{"avatar": animatedMedia.URL})
	require.Equal(t, animatedMedia.URL, body["avatar"])
	staticURL, _ := body["avatar_static"].(string)
	require.NotEqual(t, animatedMedia.URL, staticURL)
	require.Contains(t, staticURL, "/thumbnail/")
	require.NotContains(t, staticURL, fileNameFromMediaURL(animatedMedia.URL))
	saved = reloadAccount(t, conn, owner.Id)
	require.Equal(t, animatedMedia.URL, saved.Avatar)
	require.Equal(t, staticURL, saved.AvatarStatic)
	require.Equal(t, header.URL, saved.Header)
	require.Equal(t, header.URL, saved.HeaderStatic)
	thumb := thumbnailBytes(t, conn, animatedMedia.ID)
	original := originalBytes(t, conn, animatedMedia.ID)
	require.True(t, bytes.HasPrefix(thumb, []byte{0xff, 0xd8, 0xff}))
	require.NotEqual(t, original, thumb)
	require.False(t, bytes.HasPrefix(original, []byte{0xff, 0xd8, 0xff}))

	// Sending the current avatar again repairs a stale static without touching the header.
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", owner.Id).Update("avatar_static", oldStatic).Error)
	body = patchCredentials(t, owner, map[string]any{"avatar": animatedMedia.URL})
	require.Equal(t, staticURL, body["avatar_static"])
	saved = reloadAccount(t, conn, owner.Id)
	require.Equal(t, animatedMedia.URL, saved.Avatar)
	require.Equal(t, staticURL, saved.AvatarStatic)
	require.Equal(t, header.URL, saved.HeaderStatic)

	beforeFail := reloadAccount(t, conn, owner.Id)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	payload, err := json.Marshal(map[string]any{"display_name": "should not stick", "avatar": foreign.URL})
	require.NoError(t, err)
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", owner)
	UpdateCredentials(c)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	saved = reloadAccount(t, conn, owner.Id)
	require.Equal(t, beforeFail.DisplayName, saved.DisplayName)
	require.Equal(t, beforeFail.Avatar, saved.Avatar)
	require.Equal(t, beforeFail.AvatarStatic, saved.AvatarStatic)
	require.Equal(t, beforeFail.Header, saved.Header)
	require.Equal(t, beforeFail.HeaderStatic, saved.HeaderStatic)

	pending := &models.MediaAttachment{
		AccountId: owner.Id, FileName: "pending.webp", FilePath: "2026/01/02/pending.webp",
		ContentType: "image/webp", Type: models.MediaTypeImage, ProcessState: models.ProcessProcessing,
	}
	require.NoError(t, conn.Create(pending).Error)
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	payload, err = json.Marshal(map[string]any{"avatar": "http://localhost:8080/media/" + fmt.Sprint(pending.Id) + "/pending.webp"})
	require.NoError(t, err)
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", owner)
	UpdateCredentials(c)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	saved = reloadAccount(t, conn, owner.Id)
	require.Equal(t, beforeFail.Avatar, saved.Avatar)
	require.Equal(t, beforeFail.AvatarStatic, saved.AvatarStatic)
}

type uploadedProfileImage struct {
	ID  string
	URL string
}

func uploadProfileImage(t *testing.T, owner *models.Account, payload []byte, name, contentType string) uploadedProfileImage {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+name+`"`)
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/media", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req
	c.Set("authenticated", true)
	c.Set("account", owner)
	mediactl.Upload(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		ID       string `json:"id"`
		URL      string `json:"url"`
		FileName string `json:"file_name"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.URL)
	return uploadedProfileImage{ID: resp.ID, URL: resp.URL}
}

func fileNameFromMediaURL(raw string) string {
	parts := strings.Split(strings.TrimRight(raw, "/"), "/")
	return parts[len(parts)-1]
}

func patchCredentials(t *testing.T, owner *models.Account, fields map[string]any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(fields)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPatch, "/api/v1/accounts/update_credentials", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", owner)
	UpdateCredentials(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

func reloadAccount(t *testing.T, conn *gorm.DB, id uint64) models.Account {
	t.Helper()
	var saved models.Account
	require.NoError(t, conn.First(&saved, id).Error)
	return saved
}

func useAccountCache(t *testing.T) {
	t.Helper()
	accountCacheRedis.once.Do(func() {
		accountCacheRedis.server, accountCacheRedis.err = miniredis.Run()
		if accountCacheRedis.err == nil {
			accountCacheRedis.client = redis.NewClient(&redis.Options{Addr: accountCacheRedis.server.Addr()})
		}
	})
	require.NoError(t, accountCacheRedis.err)
	previousRedis := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{accountCacheRedis.client})
	t.Cleanup(func() { cRedis.SetRedisesForTest(previousRedis) })
}

// storeProcessedAnimation runs a two-frame GIF through ProcessFile and stores
// the animated result beside a JPEG of its first frame. The upload handler's
// thumbnail step cannot decode this ffmpeg's animated WebP, so the credential
// test uses the processed file and that still thumbnail directly.
func storeProcessedAnimation(t *testing.T, conn *gorm.DB, owner *models.Account) uploadedProfileImage {
	t.Helper()
	gifBytes := twoFrameGIF(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "anim.gif")
	require.NoError(t, os.WriteFile(src, gifBytes, 0o600))
	work := filepath.Join(dir, "work")
	require.NoError(t, os.Mkdir(work, 0o700))
	out, err := mediaproc.ProcessFile(context.Background(), src, work, "image/gif", systemsettings.LoadMediaProcessing(), mediaproc.DefaultLimits(systemsettings.MediaImageSizeLimit(), systemsettings.MediaVideoSizeLimit()))
	require.NoError(t, err)
	probed, err := mediaproc.ProbeFile(context.Background(), out.Path)
	require.NoError(t, err)
	require.True(t, probed.Animated)

	decoded, err := gif.DecodeAll(bytes.NewReader(gifBytes))
	require.NoError(t, err)
	require.Greater(t, len(decoded.Image), 1)
	canvas := image.NewRGBA(image.Rect(0, 0, decoded.Config.Width, decoded.Config.Height))
	draw.Draw(canvas, decoded.Image[0].Bounds(), decoded.Image[0], decoded.Image[0].Bounds().Min, draw.Over)
	var still bytes.Buffer
	require.NoError(t, jpeg.Encode(&still, canvas, &jpeg.Options{Quality: 80}))
	thumbFile := filepath.Join(dir, "frame.jpg")
	require.NoError(t, os.WriteFile(thumbFile, still.Bytes(), 0o600))

	fileName := strconv.FormatInt(time.Now().UnixNano(), 10) + "_anim.webp"
	thumbName := "thumb_" + strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".jpg"
	dateDir := time.Now().Format("2006/01/02")
	filePath := path.Join(dateDir, fileName)
	require.NoError(t, storage.PutFile(context.Background(), out.Path, models.MediaKeyPrefix+filePath, out.MIME))
	require.NoError(t, storage.PutFile(context.Background(), thumbFile, path.Join(models.MediaKeyPrefix+dateDir, thumbName), "image/jpeg"))
	row := &models.MediaAttachment{
		AccountId: owner.Id, FileName: fileName, FilePath: filePath, FileSize: out.OutputBytes,
		ContentType: out.MIME, Type: models.MediaTypeImage, ProcessState: models.ProcessReady,
		ProcessProgress: 100, ThumbnailPath: &thumbName,
	}
	require.NoError(t, conn.Create(row).Error)
	return uploadedProfileImage{
		ID:  strconv.FormatUint(row.Id, 10),
		URL: strings.TrimRight(systemsettings.GetBaseURL(), "/") + "/media/" + strconv.FormatUint(row.Id, 10) + "/" + fileName,
	}
}

func twoFrameGIF(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{color.Black, color.White, color.RGBA{R: 255, A: 255}}
	a := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
	b := image.NewPaletted(image.Rect(0, 0, 8, 8), pal)
	for i := range 8 {
		a.SetColorIndex(i, i, 1)
		b.SetColorIndex(i, i, 2)
	}
	var buf bytes.Buffer
	require.NoError(t, gif.EncodeAll(&buf, &gif.GIF{Image: []*image.Paletted{a, b}, Delay: []int{10, 10}}))
	return buf.Bytes()
}

func pngBytes(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func thumbnailBytes(t *testing.T, conn *gorm.DB, id string) []byte {
	t.Helper()
	return mediaFileBytes(t, conn, id, true)
}

func originalBytes(t *testing.T, conn *gorm.DB, id string) []byte {
	t.Helper()
	return mediaFileBytes(t, conn, id, false)
}

func mediaFileBytes(t *testing.T, conn *gorm.DB, id string, thumb bool) []byte {
	t.Helper()
	var att models.MediaAttachment
	require.NoError(t, conn.First(&att, id).Error)
	key := att.StorageKey()
	if thumb {
		key = att.ThumbnailStorageKey()
	}
	require.NotEmpty(t, key)
	raw, err := os.ReadFile(filepath.Join("uploads", filepath.FromSlash(key)))
	require.NoError(t, err)
	return raw
}

func TestUpdateCredentialsRejectsUnusableProfileImage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	useAccountCache(t)
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.AccountExtra{}, &models.Emoji{}, &models.MediaAttachment{}))
	previousDB := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previousDB) })
	owner := &models.Account{Id: 7, Username: "owner", Avatar: "http://localhost:8080/media/1/keep.png", AvatarStatic: "http://localhost:8080/media/1/keep-static.png", DisplayName: "stable"}
	require.NoError(t, conn.Create(owner).Error)
	video := &models.MediaAttachment{
		AccountId: owner.Id, FileName: "clip.webm", FilePath: "2026/01/02/clip.webm",
		ContentType: "video/webm", Type: models.MediaTypeVideo, ProcessState: models.ProcessReady,
	}
	require.NoError(t, conn.Create(video).Error)
	for _, raw := range []string{
		"http://localhost:8080/media/" + fmt.Sprint(video.Id) + "/clip.webm",
		"http://localhost:8080/media/999/missing.webp",
		"http://localhost:8080/media/" + fmt.Sprint(video.Id) + "/thumbnail/thumb.jpg",
		"not a media url",
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		payload := `{"avatar":` + jsonString(raw) + `,"display_name":"changed"}`
		c.Request = httptest.NewRequest(http.MethodPatch, "/", strings.NewReader(payload))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("account", owner)
		UpdateCredentials(c)
		require.Equal(t, http.StatusUnprocessableEntity, w.Code, raw+" "+w.Body.String())
	}
	saved := reloadAccount(t, conn, owner.Id)
	require.Equal(t, "stable", saved.DisplayName)
	require.Equal(t, owner.Avatar, saved.Avatar)
	require.Equal(t, owner.AvatarStatic, saved.AvatarStatic)
}

func jsonString(v string) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
