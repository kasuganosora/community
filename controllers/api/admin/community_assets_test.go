package admin

import (
	"bytes"
	"encoding/json"
	"github.com/alicebob/miniredis/v2"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/redis/go-redis/v9"
	"image"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func assetsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sql, err := conn.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(1)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); sql.Close() })
	require.NoError(t, conn.AutoMigrate(&models.AvatarDecoration{}, &models.Advertisement{}, &models.Account{}, &models.MediaAttachment{}))
	return conn
}
func assetRequest(handler gin.HandlerFunc, method string, id string, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "id", Value: id}}
	c.Request = httptest.NewRequest(method, "/", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	c.Writer.WriteHeaderNow()
	return w
}
func TestBrandImageUploadRejectsActiveContentAndPersistsSafeImage(t *testing.T) {
	conn := assetsTestDB(t)
	t.Chdir(t.TempDir())
	upload := func(data []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, err := writer.CreateFormFile("file", "image.png")
		require.NoError(t, err)
		_, err = part.Write(data)
		require.NoError(t, err)
		require.NoError(t, writer.WriteField("name", "Frame"))
		require.NoError(t, writer.WriteField("enabled", "true"))
		require.NoError(t, writer.Close())
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", &body)
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		CreateAvatarDecoration(c)
		return w
	}
	require.Equal(t, 422, upload([]byte(`<svg onload="alert(1)"></svg>`)).Code)
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	w := upload(buf.Bytes())
	require.Equal(t, 201, w.Code, w.Body.String())
	var row models.AvatarDecoration
	require.NoError(t, conn.First(&row).Error)
	require.Contains(t, row.ImageURL, "/emoji-assets/")
	require.True(t, row.Enabled)
	imageResponse := assetRequest(AvatarDecorationImage, "GET", "1", "")
	require.Equal(t, 200, imageResponse.Code)
	require.Equal(t, "image/png", imageResponse.Header().Get("Content-Type"))
	require.Equal(t, "no-store", imageResponse.Header().Get("Cache-Control"))
	require.Equal(t, "cross-origin", imageResponse.Header().Get("Cross-Origin-Resource-Policy"))
	require.Equal(t, 200, assetRequest(UpdateAvatarDecoration, "PATCH", "1", `{"enabled":false}`).Code)
	w = assetRequest(AvatarDecorationImage, "GET", "1", "")
	require.Equal(t, 404, w.Code)
}
func TestAdvertisementsScheduleLinksAndNoTracking(t *testing.T) {
	conn := assetsTestDB(t)
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	rows := []models.Advertisement{{Title: "active", ImageURL: "/emoji-assets/a.png", TargetURL: "https://example.test", Enabled: true, StartsAt: &past, EndsAt: &future}, {Title: "future", Enabled: true, StartsAt: &future}, {Title: "ended", Enabled: true, EndsAt: &past}, {Title: "disabled"}}
	require.NoError(t, conn.Create(&rows).Error)
	w := assetRequest(PublicAdvertisements, "GET", "", "")
	require.Equal(t, 200, w.Code)
	var list []models.Advertisement
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list, 1)
	require.Equal(t, "active", list[0].Title)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.Equal(t, 422, assetRequest(UpdateAdvertisement, "PATCH", "1", `{"target_url":"javascript:alert(1)"}`).Code)
	require.Equal(t, 422, assetRequest(UpdateAdvertisement, "PATCH", "1", `{"target_url":"https://user:password@example.test"}`).Code)
}
func TestManagedFileDeletionProtectsAttachedAndSharedFiles(t *testing.T) {
	conn := assetsTestDB(t)
	statusID := uint64(40)
	rows := []models.MediaAttachment{{Id: 1, AccountId: 1, FileName: "private.png", FilePath: "private.png", StatusId: &statusID}, {Id: 2, AccountId: 1, FileName: "shared.png", FilePath: "shared.png"}, {Id: 3, AccountId: 2, FileName: "shared.png", FilePath: "shared.png"}, {Id: 4, AccountId: 1, FileName: "free.png", FilePath: "free.png"}}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&rows).Error)
	for _, id := range []string{"1", "2", "3"} {
		require.Equal(t, 409, assetRequest(DeleteManagedFile, "DELETE", id, "").Code)
	}
	require.Equal(t, 204, assetRequest(DeleteManagedFile, "DELETE", "4", "").Code)
	var count int64
	require.NoError(t, conn.Model(&models.MediaAttachment{}).Count(&count).Error)
	require.EqualValues(t, 3, count)
	w := assetRequest(ListManagedFiles, "GET", "", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "delete_blocked")
}
func TestDeletingDecorationClearsSelections(t *testing.T) {
	conn := assetsTestDB(t)
	row := models.AvatarDecoration{Name: "Frame", ImageURL: "/emoji-assets/safe.png", Enabled: true}
	require.NoError(t, conn.Create(&row).Error)
	account := models.Account{Id: 21, Username: "member", AvatarDecorationID: row.ID}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)
	require.Equal(t, 204, assetRequest(DeleteAvatarDecoration, "DELETE", "1", "").Code)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).First(&account, account.Id).Error)
	require.Zero(t, account.AvatarDecorationID)
	require.Equal(t, 404, assetRequest(AvatarDecorationImage, "GET", "1", "").Code)
}

var assetCacheRedis struct {
	once   sync.Once
	server *miniredis.Miniredis
	client *redis.Client
	err    error
}

func TestSelectDecorationAuthorizesOnlySelfAndEnabledAssets(t *testing.T) {
	assetCacheRedis.once.Do(func() {
		assetCacheRedis.server, assetCacheRedis.err = miniredis.Run()
		if assetCacheRedis.err == nil {
			assetCacheRedis.client = redis.NewClient(&redis.Options{Addr: assetCacheRedis.server.Addr()})
		}
	})
	require.NoError(t, assetCacheRedis.err)
	previousRedis := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{assetCacheRedis.client})
	t.Cleanup(func() { cRedis.SetRedisesForTest(previousRedis) })
	conn := assetsTestDB(t)
	rows := []models.AvatarDecoration{{ID: 1, Name: "yes", ImageURL: "/emoji-assets/a.png", Enabled: true}, {ID: 2, Name: "no", ImageURL: "/emoji-assets/b.png", Enabled: false}}
	require.NoError(t, conn.Create(&rows).Error)
	account := models.Account{Id: 11, Username: "self"}
	other := models.Account{Id: 12, Username: "other"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&other).Error)
	require.Equal(t, 401, assetRequest(SelectAvatarDecoration, "PUT", "", `{"id":"1"}`).Code)
	invoke := func(body string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("account", &account)
		c.Request = httptest.NewRequest("PUT", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		SelectAvatarDecoration(c)
		c.Writer.WriteHeaderNow()
		return w.Code
	}
	require.Equal(t, 404, invoke(`{"id":"2"}`))
	require.Equal(t, 200, invoke(`{"id":"1","account_id":"2"}`))
	var got models.Account
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).First(&got, account.Id).Error)
	require.EqualValues(t, 1, got.AvatarDecorationID)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).First(&other, other.Id).Error)
	require.Zero(t, other.AvatarDecorationID)
	require.Equal(t, 200, invoke(`{"id":"0"}`))
}
