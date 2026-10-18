package account

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/twofactor"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type tfEnv struct {
	conn *gorm.DB
	u    *models.User
	a    *models.Account
}

func newTwoFactorEnv(t *testing.T) *tfEnv {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true, Logger: logger.Discard})
	require.NoError(t, err)
	sql, _ := conn.DB()
	sql.SetMaxOpenConns(1)
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sql.Close() })
	require.NoError(t, conn.AutoMigrate(&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.UserSecretData{},
		&models.SystemSetting{}, &models.AuthToken{}, &models.UserTwoFactor{}, &models.UserBackupCode{},
		&models.Role{}, &models.RoleUser{}))
	systemsettings.Set(systemsettings.KeyRegistrationCheckEmail, false)
	u := &models.User{Id: 1, UserName: "qa", EmailHash: "qa-hash", Status: models.UserStatusNormal}
	require.NoError(t, conn.Create(u).Error)
	a := &models.Account{Id: 1, UserId: u.Id, Username: "qa"}
	require.NoError(t, conn.Create(a).Error)
	password, err := models.EncryptPassword("correct-password", "salt")
	require.NoError(t, err)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.UserSecretData{UserId: u.Id, Password: password, Salt: "salt"}).Error)

	return &tfEnv{conn: conn, u: u, a: a}
}

func (e *tfEnv) call(handler gin.HandlerFunc, body map[string]any, web bool) (int, map[string]any) {
	encoded, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/x", bytes.NewReader(encoded))
	c.Request.Header.Set("Content-Type", "application/json")
	if web {
		c.Set("account", e.a)
		c.Set("user", e.u)
		c.Set("token_data", &models.AuthTokenData{UserId: e.u.Id, AuthType: models.AuthTokenTypeWeb})
	}
	handler(c)
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestLoginWithoutTwoFactor(t *testing.T) {
	e := newTwoFactorEnv(t)
	code, body := e.call(Login, map[string]any{"username": "qa", "password": "correct-password"}, false)
	require.Equal(t, 200, code, body)
	require.NotEmpty(t, body["token"])
}

func TestTwoFactorSetupLoginAndDisable(t *testing.T) {
	e := newTwoFactorEnv(t)

	code, body := e.call(TwoFactorSetup, map[string]any{"password": "wrong"}, true)
	require.Equal(t, 422, code)
	code, body = e.call(TwoFactorSetup, map[string]any{"password": "correct-password"}, true)
	require.Equal(t, 200, code, body)
	secret := body["secret"].(string)
	require.True(t, strings.HasPrefix(body["otpauth_uri"].(string), "otpauth://totp/"))

	totp, _ := twofactor.Generate(secret, time.Now())
	code, body = e.call(TwoFactorConfirm, map[string]any{"code": totp}, true)
	require.Equal(t, 200, code, body)
	backups := body["backup_codes"].([]any)
	require.Len(t, backups, twofactor.BackupCodeCount)

	// password alone is no longer enough
	code, body = e.call(Login, map[string]any{"username": "qa", "password": "correct-password"}, false)
	require.Equal(t, 401, code)
	require.Equal(t, "two_factor_required", body["error"])
	require.Nil(t, body["token"])
	challenge := body["challenge"].(string)

	// wrong password never reveals the 2FA step
	code, body = e.call(Login, map[string]any{"username": "qa", "password": "nope"}, false)
	require.Equal(t, 401, code)
	require.Nil(t, body["challenge"])

	code, _ = e.call(LoginTwoFactor, map[string]any{"challenge": challenge, "code": "000000"}, false)
	require.Equal(t, 401, code)
	code, _ = e.call(LoginTwoFactor, map[string]any{"challenge": "forged", "code": backups[0]}, false)
	require.Equal(t, 401, code)

	// backup code works once
	code, body = e.call(LoginTwoFactor, map[string]any{"challenge": challenge, "code": backups[0]}, false)
	require.Equal(t, 200, code, body)
	require.NotEmpty(t, body["token"])
	code, _ = e.call(LoginTwoFactor, map[string]any{"challenge": challenge, "code": backups[0]}, false)
	require.Equal(t, 401, code)

	// single-request form with otp_attempt
	code, body = e.call(Login, map[string]any{"username": "qa", "password": "correct-password", "otp_attempt": backups[1]}, false)
	require.Equal(t, 200, code, body)

	// sensitive actions need the code too
	code, body = e.call(TwoFactorDisable, map[string]any{"password": "correct-password"}, true)
	require.Equal(t, 403, code, body)
	code, _ = e.call(TwoFactorDisable, map[string]any{"password": "wrong", "code": backups[2]}, true)
	require.Equal(t, 422, code)

	code, body = e.call(TwoFactorRegenerateBackupCodes, map[string]any{"password": "correct-password", "code": backups[2]}, true)
	require.Equal(t, 200, code, body)
	fresh := body["backup_codes"].([]any)
	code, _ = e.call(TwoFactorDisable, map[string]any{"password": "correct-password", "code": backups[3]}, true)
	require.Equal(t, 422, code, "old backup codes are invalid after regeneration")

	code, body = e.call(TwoFactorDisable, map[string]any{"password": "correct-password", "code": fresh[0]}, true)
	require.Equal(t, 200, code, body)
	code, _ = e.call(Login, map[string]any{"username": "qa", "password": "correct-password"}, false)
	require.Equal(t, 200, code)
}

func TestTwoFactorEndpointsRequireWebSession(t *testing.T) {
	e := newTwoFactorEnv(t)
	for _, h := range []gin.HandlerFunc{TwoFactorStatus, TwoFactorSetup, TwoFactorConfirm, TwoFactorDisable, TwoFactorRegenerateBackupCodes} {
		code, _ := e.call(h, map[string]any{}, false)
		require.Equal(t, 401, code)
	}
}
