package account

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLifecycleRejectsDelegatedTokensAndRequiresReauthentication(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	sql, err := conn.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(1)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sql.Close() })
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.UserSecretData{}, &models.AccountDeletion{}, &models.SystemSetting{}))
	u := &models.User{Id: 1, UserName: "alice", EmailHash: "alice-hash"}
	require.NoError(t, conn.Create(u).Error)
	a := &models.Account{Id: 1, UserId: u.Id, Username: "alice"}
	require.NoError(t, conn.Create(a).Error)
	password, err := models.EncryptPassword("correct-password", "salt")
	require.NoError(t, err)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.UserSecretData{UserId: u.Id, Password: password, Salt: "salt"}).Error)
	request := func(handler gin.HandlerFunc, typ uint8, body map[string]any) *httptest.ResponseRecorder {
		encoded, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/v1/accounts/lifecycle", bytes.NewReader(encoded))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Set("account", a)
		c.Set("user", u)
		c.Set("token_data", &models.AuthTokenData{UserId: u.Id, AuthType: typ})
		handler(c)
		return w
	}
	for _, handler := range []gin.HandlerFunc{Lifecycle, UpdateAliases, MoveAccount, RequestAccountDeletion} {
		require.Equal(t, 403, request(handler, models.AuthTokenTypeOauth, map[string]any{}).Code)
	}
	bad := request(RequestAccountDeletion, models.AuthTokenTypeWeb, map[string]any{"password": "correct-password", "confirmation": "bob", "confirm": true})
	require.Equal(t, 422, bad.Code)
	bad = request(RequestAccountDeletion, models.AuthTokenTypeWeb, map[string]any{"password": "wrong-password", "confirmation": "alice", "confirm": true})
	require.Equal(t, 422, bad.Code)
	require.Contains(t, bad.Body.String(), "invalid password")
	bad = request(MoveAccount, models.AuthTokenTypeWeb, map[string]any{"password": "correct-password", "target": "bob"})
	require.Equal(t, 422, bad.Code)
	bad = request(MoveAccount, models.AuthTokenTypeWeb, map[string]any{"password": "wrong-password", "target": "bob", "confirm": true})
	require.Equal(t, 422, bad.Code)
	require.Contains(t, bad.Body.String(), "invalid password")
	var count int64
	require.NoError(t, conn.Model(&models.AccountDeletion{}).Count(&count).Error)
	require.Zero(t, count)
	var saved models.Account
	require.NoError(t, conn.First(&saved, a.Id).Error)
	require.False(t, saved.IsBan)
	require.Empty(t, saved.MovedTo)
}
