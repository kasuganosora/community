package account

import (
	"encoding/json"
	"net/http/httptest"
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

func TestMediaDisplayPreference(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, _ := conn.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.UserSetting{}))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)

	acc := &models.Account{Id: 1, User: &models.User{Id: 7}}
	call := func(h gin.HandlerFunc, body string) (int, map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("PATCH", "/api/v1/preferences", strings.NewReader(body))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("account", acc)
		h(c)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)

		return w.Code, out
	}
	code, out := call(GetPreferences, "")
	assert.Equal(t, 200, code)
	assert.Equal(t, "default", out["reading:expand:media"])
	for _, mode := range []string{"hide_all", "show_all", "default"} {
		code, out = call(UpdatePreferences, `{"reading:expand:media":"`+mode+`"}`)
		require.Equal(t, 200, code)
		assert.Equal(t, mode, out["reading:expand:media"])
		var saved models.UserSetting
		require.NoError(t, conn.Where("user_id = ?", 7).First(&saved).Error)
		assert.Equal(t, mode, saved.Data["reading_expand_media"])
	}
	code, _ = call(UpdatePreferences, `{"reading:expand:media":"sometimes"}`)
	assert.Equal(t, 422, code)
}
