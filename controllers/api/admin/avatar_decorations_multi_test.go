package admin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSelectAvatarDecorationsHonoursLimitAndGeometry(t *testing.T) {
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
	require.NoError(t, conn.AutoMigrate(&models.SystemSetting{}))
	rows := []models.AvatarDecoration{{ID: 1, Name: "a", ImageURL: "/emoji-assets/a.png", Enabled: true}, {ID: 2, Name: "b", ImageURL: "/emoji-assets/b.png", Enabled: true}, {ID: 3, Name: "off", ImageURL: "/emoji-assets/c.png"}}
	require.NoError(t, conn.Create(&rows).Error)
	account := models.Account{Id: 21, Username: "self"}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&account).Error)
	invoke := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("account", &account)
		c.Request = httptest.NewRequest("PUT", "/", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		SelectAvatarDecorations(c)
		c.Writer.WriteHeaderNow()
		return w
	}
	systemsettings.Set(KeyAvatarDecorationLimit, 1)
	t.Cleanup(func() { systemsettings.Set(KeyAvatarDecorationLimit, 1) })
	require.Equal(t, 1, AvatarDecorationLimit())
	require.Equal(t, 422, invoke(`{"decorations":[{"id":"1"},{"id":"2"}]}`).Code)

	systemsettings.Set(KeyAvatarDecorationLimit, 2)
	require.Equal(t, 422, invoke(`{"decorations":[{"id":"3"}]}`).Code)
	w := invoke(`{"decorations":[{"id":"2","angle":0.9,"flip_h":true,"offset_x":0.1,"offset_y":-0.3},{"id":"1"}]}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	var resp struct {
		AvatarDecorations []map[string]any `json:"avatar_decorations"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.AvatarDecorations, 2)
	require.Equal(t, 0.5, resp.AvatarDecorations[0]["angle"])
	require.Equal(t, -0.25, resp.AvatarDecorations[0]["offset_y"])

	var got models.Account
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).First(&got, account.Id).Error)
	require.EqualValues(t, 2, got.AvatarDecorationID)
	require.Len(t, got.AvatarDecorations, 2)

	// Deleting a decoration removes it from lists and the legacy field.
	require.Equal(t, 204, assetRequest(DeleteAvatarDecoration, "DELETE", "2", "").Code)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).First(&got, account.Id).Error)
	require.EqualValues(t, 1, got.AvatarDecorationID)
	require.Equal(t, models.AvatarDecorationSelections{{ID: "1"}}, got.AvatarDecorations)

	require.Equal(t, 200, invoke(`{"decorations":[]}`).Code)
	got = models.Account{}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).First(&got, account.Id).Error)
	require.Zero(t, got.AvatarDecorationID)
	require.Nil(t, got.AvatarDecorations)
}

func TestAvatarDecorationSettingsValidation(t *testing.T) {
	assetsTestDB(t)
	require.Equal(t, 422, assetRequest(UpdateAvatarDecorationSettings, "PUT", "", `{"limit":0}`).Code)
	require.Equal(t, 422, assetRequest(UpdateAvatarDecorationSettings, "PUT", "", `{"limit":17}`).Code)
	require.Equal(t, 400, assetRequest(UpdateAvatarDecorationSettings, "PUT", "", `{}`).Code)
}
