package account

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestCustomEmojiCatalogueRevalidatesWithoutTheFullList(t *testing.T) {
	conn := emojiCatalogueDB(t)
	wave := models.Emoji{
		Shortcode: "wave", Url: "https://cdn.test/emoji-assets/wave.png", StaticUrl: "https://cdn.test/emoji-assets/wave.png",
		VisibleInPicker: true, Category: "greet", Aliases: []string{"hello"}, IsSensitive: true,
	}
	require.NoError(t, conn.Create(&wave).Error)
	require.NoError(t, conn.Model(&wave).UpdateColumns(map[string]any{"updated_at": int64(100), "created_at": int64(100)}).Error)
	require.NoError(t, conn.Create(&models.Emoji{Shortcode: "remote", Domain: "remote.test", Url: "https://remote.test/a.png", StaticUrl: "https://remote.test/a.png"}).Error)
	require.NoError(t, conn.Create(&models.Emoji{Shortcode: "off", Url: "https://cdn.test/emoji-assets/off.png", StaticUrl: "https://cdn.test/emoji-assets/off.png", Disabled: true}).Error)

	w := getEmojis(t)
	require.Equal(t, http.StatusOK, w.Code)
	etag := w.Header().Get("ETag")
	require.Equal(t, `W/"1.1.100.100"`, etag)
	require.Equal(t, "public, no-cache", w.Header().Get("Cache-Control"))
	var body []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body, 1)
	require.Equal(t, "wave", body[0]["shortcode"])
	require.Equal(t, []any{"hello"}, body[0]["aliases"])
	require.Equal(t, true, body[0]["is_sensitive"])
	require.Equal(t, "greet", body[0]["category"])

	notModified := getEmojis(t, etag)
	require.Equal(t, http.StatusNotModified, notModified.Code)
	require.Empty(t, notModified.Body.String())
	require.Equal(t, etag, notModified.Header().Get("ETag"))
	require.Equal(t, http.StatusNotModified, getEmojis(t, "*").Code)
	require.Equal(t, http.StatusNotModified, getEmojis(t, strings.TrimPrefix(etag, "W/")).Code)
	require.Equal(t, http.StatusNotModified, getEmojis(t, etag+`, W/"other"`).Code)

	require.NoError(t, conn.Create(&models.Emoji{Shortcode: "elsewhere", Domain: "elsewhere.test", Url: "https://elsewhere.test/a.png", StaticUrl: "https://elsewhere.test/a.png"}).Error)
	require.Equal(t, http.StatusNotModified, getEmojis(t, etag).Code, "remote rows are outside the public catalogue")

	require.NoError(t, conn.Model(&models.Emoji{}).Where("shortcode = ?", "wave").UpdateColumns(map[string]any{"category": "new", "updated_at": int64(101)}).Error)
	changed := getEmojis(t, etag)
	require.Equal(t, http.StatusOK, changed.Code)
	require.NotEqual(t, etag, changed.Header().Get("ETag"))
	require.NoError(t, json.Unmarshal(changed.Body.Bytes(), &body))
	require.Equal(t, "new", body[0]["category"])

	next := changed.Header().Get("ETag")
	require.NoError(t, conn.Model(&models.Emoji{}).Where("shortcode = ?", "wave").UpdateColumns(map[string]any{"disabled": true, "updated_at": int64(102)}).Error)
	afterDelete := getEmojis(t, next)
	require.Equal(t, http.StatusOK, afterDelete.Code)
	require.NotEqual(t, next, afterDelete.Header().Get("ETag"))
	require.Equal(t, `W/"0.0.0.0"`, afterDelete.Header().Get("ETag"))
	require.NoError(t, json.Unmarshal(afterDelete.Body.Bytes(), &body))
	require.Empty(t, body)

	added := models.Emoji{Shortcode: "blob", Url: "https://cdn.test/emoji-assets/blob.png", StaticUrl: "https://cdn.test/emoji-assets/blob.png", VisibleInPicker: true}
	require.NoError(t, conn.Create(&added).Error)
	require.NoError(t, conn.Model(&added).UpdateColumns(map[string]any{"updated_at": int64(200), "created_at": int64(200)}).Error)
	withBlob := getEmojis(t, afterDelete.Header().Get("ETag"))
	require.Equal(t, http.StatusOK, withBlob.Code)
	require.NoError(t, json.Unmarshal(withBlob.Body.Bytes(), &body))
	require.Len(t, body, 1)
	require.Equal(t, "blob", body[0]["shortcode"])
	require.Equal(t, http.StatusNotModified, getEmojis(t, withBlob.Header().Get("ETag")).Code)
}

func emojiCatalogueDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Emoji{}))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	return conn
}

func getEmojis(t *testing.T, match ...string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/custom_emojis", nil)
	if len(match) == 1 {
		c.Request.Header.Set("If-None-Match", match[0])
	}
	GetCustomEmojis(c)
	return w
}
