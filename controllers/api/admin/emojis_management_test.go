package admin

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/controllers/api/account"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func emojiManagementDB(t *testing.T) *gorm.DB {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	for _, model := range []any{&models.Emoji{}, &models.EmojiUpload{}, &models.Role{}} {
		stmt := &gorm.Statement{DB: conn}
		require.NoError(t, stmt.Parse(model))
		for _, field := range stmt.Schema.Fields {
			if field.AutoIncrement {
				field.DataType = schema.Int
			}
		}
		require.NoError(t, conn.AutoMigrate(model))
	}
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	return conn
}
func TestEmojiManagementValidatedUploadMetadataAndIsolation(t *testing.T) {
	conn := emojiManagementDB(t)
	// Upload actual PNG bytes, then use the returned opaque asset ID.
	var data bytes.Buffer
	form := multipart.NewWriter(&data)
	file, err := form.CreateFormFile("file", "tiny.png")
	require.NoError(t, err)
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	require.NoError(t, png.Encode(file, img))
	require.NoError(t, form.Close())
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/upload", &data)
	c.Request.Header.Set("Content-Type", form.FormDataContentType())
	c.Set("account", &models.Account{Id: 7})
	UploadCustomEmoji(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var upload models.EmojiUpload
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &upload))
	t.Cleanup(func() {
		require.NoError(t, emojiasset.DeleteAsset(&emojiasset.Asset{URL: upload.Url, StaticURL: upload.StaticUrl}))
	})
	require.NotEmpty(t, upload.Id)
	require.Equal(t, "image/png", upload.ContentType)
	require.Positive(t, upload.FileSize)
	require.NoError(t, conn.Create(&models.Role{ID: 9, Name: "reviewer"}).Error)
	body := map[string]any{"shortcode": "wave", "image_id": upload.Id, "aliases": []string{"hello", "hello", "你好"}, "license": "CC0", "category": "greetings", "local_only": true, "is_sensitive": true, "reaction_role_ids": []string{"9"}}
	c, w = newJSONContext(t, "POST", nil, body)
	c.Set("account", &models.Account{Id: 8})
	CreateCustomEmoji(c)
	require.Equal(t, 404, w.Code)
	c, w = newJSONContext(t, "POST", nil, body)
	c.Set("account", &models.Account{Id: 7})
	CreateCustomEmoji(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var emoji models.Emoji
	require.NoError(t, conn.First(&emoji).Error)
	require.Equal(t, []string{"hello", "你好"}, emoji.Aliases)
	require.Equal(t, []uint64{9}, emoji.ReactionRoleIDs)
	require.True(t, emoji.LocalOnly)
	require.True(t, emoji.IsSensitive)
	// Same shortcode is allowed on another server, but never twice locally.
	require.NoError(t, conn.Create(&models.Emoji{Shortcode: "wave", Domain: "remote.example", Url: "https://remote.example/a.png", StaticUrl: "https://remote.example/a.png"}).Error)
	c, w = newJSONContext(t, "POST", nil, body)
	c.Set("account", &models.Account{Id: 7})
	CreateCustomEmoji(c)
	require.Equal(t, 422, w.Code, w.Body.String())
	params := gin.Params{{Key: "id", Value: strconv.FormatUint(emoji.Id, 10)}}
	c, w = newJSONContext(t, "PUT", params, map[string]any{"category": "", "aliases": []string{}, "license": "", "local_only": false, "is_sensitive": false, "visible_in_picker": false, "reaction_role_ids": []string{}})
	UpdateCustomEmoji(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NoError(t, conn.First(&emoji, emoji.Id).Error)
	require.Empty(t, emoji.Category)
	require.Empty(t, emoji.Aliases)
	require.Empty(t, emoji.License)
	require.False(t, emoji.LocalOnly)
	require.False(t, emoji.IsSensitive)
	require.Empty(t, emoji.ReactionRoleIDs)
	c, w = newGetContext(t, nil, "")
	account.GetCustomEmojis(c)
	require.Equal(t, 200, w.Code)
	var public []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &public))
	require.Len(t, public, 1)
	require.False(t, public[0]["visible_in_picker"].(bool))
	// Deletion releases local identity without rewriting a prior emoji's row.
	c, w = newDeleteContext(t, params)
	DeleteCustomEmoji(c)
	require.Equal(t, 200, w.Code)
	c, w = newJSONContext(t, "POST", nil, body)
	c.Set("account", &models.Account{Id: 7})
	CreateCustomEmoji(c)
	require.Equal(t, 200, w.Code, w.Body.String())
}
func TestEmojiBatchAtomicAndSearchPagination(t *testing.T) {
	conn := emojiManagementDB(t)
	local := models.Emoji{Shortcode: "one", Url: "x", StaticUrl: "x", Aliases: []string{"match"}}
	remote := models.Emoji{Shortcode: "two", Domain: "remote.example", Url: "x", StaticUrl: "x"}
	require.NoError(t, conn.Create(&local).Error)
	require.NoError(t, conn.Create(&remote).Error)
	c, w := newJSONContext(t, "POST", nil, map[string]any{"ids": []string{strconv.FormatUint(local.Id, 10), strconv.FormatUint(remote.Id, 10)}, "action": "set_category", "category": "changed"})
	BatchCustomEmojis(c)
	require.Equal(t, 422, w.Code)
	require.NoError(t, conn.First(&local, local.Id).Error)
	require.Empty(t, local.Category)
	c, w = newJSONContext(t, "POST", nil, map[string]any{"ids": []string{strconv.FormatUint(local.Id, 10), strconv.FormatUint(remote.Id, 10)}, "action": "disable"})
	BatchCustomEmojis(c)
	require.Equal(t, 200, w.Code)
	c, w = newGetContext(t, nil, "q=match&origin=local&page=1&limit=1")
	ListCustomEmojis(c)
	require.Equal(t, 200, w.Code)
	require.Equal(t, "1", w.Header().Get("X-Total-Count"))
	var found []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &found))
	require.Len(t, found, 1)
	require.Equal(t, "one", found[0]["shortcode"])
	c, w = newGetContext(t, nil, "page=2&limit=1")
	ListCustomEmojis(c)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &found))
	require.Len(t, found, 1)
	require.Equal(t, "two", found[0]["shortcode"])
	c, w = newJSONContext(t, "POST", nil, map[string]any{"shortcode": "unsafe", "image": "http://127.0.0.1/private"})
	CreateCustomEmoji(c)
	require.Equal(t, 400, w.Code)
}
