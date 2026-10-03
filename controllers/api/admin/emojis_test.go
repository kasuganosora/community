package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============== serializeAdminEmoji ==============

func TestSerializeAdminEmoji_Local(t *testing.T) {
	emoji := &models.Emoji{
		Id:              5,
		Shortcode:       "blob",
		Url:             "https://cdn.example.com/blob.png",
		StaticUrl:       "https://cdn.example.com/blob.png",
		VisibleInPicker: true,
		Category:        "fun",
		Domain:          "",
	}

	result := serializeAdminEmoji(emoji)

	assert.Equal(t, "5", result["id"])
	assert.Equal(t, "blob", result["shortcode"])
	assert.True(t, result["visible_in_picker"].(bool))
	assert.False(t, result["disabled"].(bool))
	assert.Nil(t, result["domain"]) // empty domain => nil
	assert.Contains(t, result["uri"].(string), "/emojis/blob")
}

func TestSerializeAdminEmoji_Remote(t *testing.T) {
	emoji := &models.Emoji{
		Id:              6,
		Domain:          "remote.social",
		Shortcode:       "wave",
		VisibleInPicker: false,
	}

	result := serializeAdminEmoji(emoji)

	assert.Equal(t, "remote.social", result["domain"])
	assert.False(t, result["disabled"].(bool)) // Picker visibility does not disable use.
}

func TestSerializeAdminEmoji_Timestamps(t *testing.T) {
	emoji := &models.Emoji{
		Id:        1,
		Shortcode: "t",
		CreatedAt: 1700000000,
		UpdatedAt: 1700000000,
	}

	result := serializeAdminEmoji(emoji)
	assert.Contains(t, result["created_at"], "2023")
	assert.Contains(t, result["updated_at"], "2023")
}

// ============== ListCustomEmojis ==============

func TestListCustomEmojis_Empty(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, nil, "")
	ListCustomEmojis(c)

	assert.Equal(t, 200, w.Code)
	var resp []any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp)
}

func TestListCustomEmojis_WithResults(t *testing.T) {
	env := newAdminTestEnv(t)
	createEmojiRow(t, env, "a", "")
	createEmojiRow(t, env, "b", "")

	c, w := newGetContext(t, nil, "")
	ListCustomEmojis(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 2)
}

func TestListCustomEmojis_FilterByShortcode(t *testing.T) {
	env := newAdminTestEnv(t)
	createEmojiRow(t, env, "alpha", "")
	createEmojiRow(t, env, "beta", "")

	c, w := newGetContext(t, nil, "shortcode=alph")
	ListCustomEmojis(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp, 1)
	assert.Equal(t, "alpha", resp[0]["shortcode"])
}

func TestListCustomEmojis_FilterByCategory(t *testing.T) {
	env := newAdminTestEnv(t)
	createEmojiRow(t, env, "cat1", "")

	c, w := newGetContext(t, nil, "category=test")
	ListCustomEmojis(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp)
}

func TestListCustomEmojis_FilterByVisible(t *testing.T) {
	env := newAdminTestEnv(t)
	createEmojiRow(t, env, "vis", "")

	c, w := newGetContext(t, nil, "visible_in_picker=true")
	ListCustomEmojis(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp)

	c2, w2 := newGetContext(t, nil, "visible_in_picker=false")
	ListCustomEmojis(c2)
	assert.Equal(t, 200, w2.Code)
}

func TestListCustomEmojis_FilterByOrigin(t *testing.T) {
	env := newAdminTestEnv(t)
	createEmojiRow(t, env, "local1", "")
	createEmojiRow(t, env, "remote1", "remote.social")

	c, w := newGetContext(t, nil, "origin=local")
	ListCustomEmojis(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)

	c2, w2 := newGetContext(t, nil, "origin=remote")
	ListCustomEmojis(c2)
	assert.Equal(t, 200, w2.Code)
}

func TestListCustomEmojis_Limit(t *testing.T) {
	env := newAdminTestEnv(t)
	createEmojiRow(t, env, "e1", "")
	createEmojiRow(t, env, "e2", "")

	c, w := newGetContext(t, nil, "limit=1")
	ListCustomEmojis(c)

	assert.Equal(t, 200, w.Code)
	var resp []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp, 1)
}

// ============== GetCustomEmoji ==============

func TestGetCustomEmoji_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	emoji := createEmojiRow(t, env, "getme", "")

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(emoji.Id)}}, "")
	GetCustomEmoji(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "getme", resp["shortcode"])
}

func TestGetCustomEmoji_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "999999"}}, "")
	GetCustomEmoji(c)
	assert.Equal(t, 404, w.Code)
}

// ============== CreateCustomEmoji ==============

func TestCreateCustomEmoji_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	require.NoError(t, env.gdb.AutoMigrate(&models.EmojiUpload{}))
	require.NoError(t, env.gdb.Create(&models.EmojiUpload{Id: "validated", AccountId: 1, Url: "https://local.example/emoji-assets/test.png", StaticUrl: "https://local.example/emoji-assets/test.png"}).Error)
	c, w := newJSONContext(t, http.MethodPost, nil, CreateEmojiRequest{Shortcode: "newone", Category: "cat", ImageID: "validated"})
	c.Set("account", &models.Account{Id: 1})
	CreateCustomEmoji(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "newone", resp["shortcode"])
}

func TestCreateCustomEmoji_DuplicateShortcode(t *testing.T) {
	env := newAdminTestEnv(t)
	createEmojiRow(t, env, "dup", "")

	c, w := newJSONContext(t, http.MethodPost, nil, CreateEmojiRequest{
		Shortcode: "dup",
		ImageID:   "",
	})
	c.Set("account", &models.Account{Id: 1})
	CreateCustomEmoji(c)
	assert.Equal(t, 422, w.Code)
}

func TestCreateCustomEmoji_MissingShortcode(t *testing.T) {
	newAdminTestEnv(t)
	c := ginContextWithBody(t, http.MethodPost, nil, `{"category":"x"}`)
	CreateCustomEmoji(c)
	assert.Equal(t, 422, c.Writer.Status())
}

func TestCreateCustomEmoji_InvalidJSON(t *testing.T) {
	newAdminTestEnv(t)
	c := ginContextWithBody(t, http.MethodPost, nil, "{not json")
	CreateCustomEmoji(c)
	assert.Equal(t, 400, c.Writer.Status())
}

// ============== UpdateCustomEmoji ==============

func TestUpdateCustomEmoji_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	emoji := createEmojiRow(t, env, "update", "")

	c, w := newJSONContext(t, http.MethodPut, gin.Params{{Key: "id", Value: uitoa(emoji.Id)}}, UpdateEmojiRequest{
		Shortcode: emojiStringPtr("updated"),
		Category:  emojiStringPtr("newcat"),
	})

	UpdateCustomEmoji(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "updated", resp["shortcode"])
	assert.Equal(t, "newcat", resp["category"])
}

func TestUpdateCustomEmoji_SetVisible(t *testing.T) {
	env := newAdminTestEnv(t)
	emoji := createEmojiRow(t, env, "visup", "")
	emoji.VisibleInPicker = true
	require.NoError(t, env.gdb.Save(emoji).Error)

	val := false
	c, w := newJSONContext(t, http.MethodPut, gin.Params{{Key: "id", Value: uitoa(emoji.Id)}}, UpdateEmojiRequest{
		VisibleInPicker: &val,
	})

	UpdateCustomEmoji(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["visible_in_picker"].(bool))
}

func TestUpdateCustomEmoji_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPut, gin.Params{{Key: "id", Value: "999999"}}, UpdateEmojiRequest{Shortcode: emojiStringPtr("x")})
	UpdateCustomEmoji(c)
	assert.Equal(t, 404, w.Code)
}

func TestUpdateCustomEmoji_InvalidJSON(t *testing.T) {
	env := newAdminTestEnv(t)
	emoji := createEmojiRow(t, env, "inv", "")

	c := ginContextWithBody(t, http.MethodPut, gin.Params{{Key: "id", Value: uitoa(emoji.Id)}}, "{bad")
	UpdateCustomEmoji(c)
	assert.Equal(t, 400, c.Writer.Status())
}

// ============== DeleteCustomEmoji ==============

func TestDeleteCustomEmoji_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	emoji := createEmojiRow(t, env, "delete", "")

	c, w := newDeleteContext(t, gin.Params{{Key: "id", Value: uitoa(emoji.Id)}})
	DeleteCustomEmoji(c)

	assert.Equal(t, 200, w.Code)
}

func TestDeleteCustomEmoji_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newDeleteContext(t, gin.Params{{Key: "id", Value: "888888"}})
	DeleteCustomEmoji(c)
	assert.Equal(t, 404, w.Code)
}

// ============== CopyCustomEmoji ==============

func TestCopyCustomEmoji_RejectsLocalCopy(t *testing.T) {
	env := newAdminTestEnv(t)
	emoji := createEmojiRow(t, env, "orig", "")

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(emoji.Id)}}, CopyEmojiRequest{
		Shortcode: "copied",
	})

	CopyCustomEmoji(c)

	assert.Equal(t, 422, w.Code)
}

func TestCopyCustomEmoji_DuplicateShortcode(t *testing.T) {
	env := newAdminTestEnv(t)
	emoji := createEmojiRow(t, env, "tobecopy", "")

	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(emoji.Id)}}, CopyEmojiRequest{
		Shortcode: "tobecopy",
	})

	CopyCustomEmoji(c)
	assert.Equal(t, 422, w.Code)
}

func TestCopyCustomEmoji_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newJSONContext(t, http.MethodPost, gin.Params{{Key: "id", Value: "777777"}}, CopyEmojiRequest{Shortcode: "x"})
	CopyCustomEmoji(c)
	assert.Equal(t, 404, w.Code)
}

func TestCopyCustomEmoji_MissingShortcode(t *testing.T) {
	env := newAdminTestEnv(t)
	emoji := createEmojiRow(t, env, "ms", "")

	c := ginContextWithBody(t, http.MethodPost, gin.Params{{Key: "id", Value: uitoa(emoji.Id)}}, `{}`)
	CopyCustomEmoji(c)
	assert.Equal(t, 422, c.Writer.Status())
}

// ============== EnableCustomEmoji / DisableCustomEmoji ==============

func TestEnableCustomEmoji_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	emoji := createEmojiRow(t, env, "en", "")
	emoji.VisibleInPicker = false
	require.NoError(t, env.gdb.Save(emoji).Error)

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(emoji.Id)}}, "")
	EnableCustomEmoji(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["disabled"].(bool))
	assert.False(t, resp["visible_in_picker"].(bool))
}

func TestEnableCustomEmoji_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "666666"}}, "")
	EnableCustomEmoji(c)
	assert.Equal(t, 404, w.Code)
}

func TestDisableCustomEmoji_Success(t *testing.T) {
	env := newAdminTestEnv(t)
	emoji := createEmojiRow(t, env, "dis", "")

	c, w := newGetContext(t, gin.Params{{Key: "id", Value: uitoa(emoji.Id)}}, "")
	DisableCustomEmoji(c)

	assert.Equal(t, 200, w.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["disabled"].(bool))
	assert.True(t, resp["visible_in_picker"].(bool))
}

func TestDisableCustomEmoji_NotFound(t *testing.T) {
	newAdminTestEnv(t)
	c, w := newGetContext(t, gin.Params{{Key: "id", Value: "555555"}}, "")
	DisableCustomEmoji(c)
	assert.Equal(t, 404, w.Code)
}

func emojiStringPtr(value string) *string { return &value }
