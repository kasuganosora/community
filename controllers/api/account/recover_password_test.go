package account

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func newRecoveryRouter() *gin.Engine {
	r := newPersistentRouter()
	r.POST("/api/v1/accounts/reset_password/verify_token", RecoverPassword)

	return r
}

func issueRecoveryToken(t *testing.T, conn *gorm.DB, userID uint64, expire time.Time) string {
	t.Helper()
	require.NoError(t, conn.AutoMigrate(&models.UserMeta{}))
	token := strings.Repeat("b", 32) + "-" + strconv.FormatUint(userID, 10) + "-" + uuid.NewString()
	require.NoError(t, models.SaveUserMeta(conn, userID, "recover_password_verify_email_token", RecoverPasswordVerifyEmailToken{
		Token:    token,
		ExpireAt: expire.Unix(),
	}))

	return token
}

func callRecover(handler http.Handler, token, password string) *httptest.ResponseRecorder {
	query := url.Values{}
	query.Set("Token", token)
	query.Set("Password", password)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/reset_password/verify_token?"+query.Encode(), nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	return w
}

func liveAuthCount(t *testing.T, conn *gorm.DB, userID uint64, kinds ...uint8) int64 {
	t.Helper()
	// []uint8 is a []byte, which the driver binds as one blob instead of an IN list.
	types := make([]int, len(kinds))
	for i, kind := range kinds {
		types[i] = int(kind)
	}
	var n int64
	require.NoError(t, conn.Model(&models.AuthToken{}).
		Where("user_id = ? AND revoke IS NULL AND type IN ?", userID, types).
		Count(&n).Error)

	return n
}

func setupRecoveryAccount(t *testing.T) (*tfEnv, *gin.Engine) {
	t.Helper()
	e := newTwoFactorEnv(t)
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "recovery-test-secret-key-32bytes"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })

	return e, newRecoveryRouter()
}

func loginAccount(t *testing.T, handler http.Handler, username, password string) (*http.Cookie, string) {
	t.Helper()
	w, body := performJSONRequest(handler, http.MethodPost, "/api/v1/accounts/login", map[string]any{
		"username": username,
		"password": password,
	}, nil, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	cookie := extractAppTokenCookie(w)
	require.NotNil(t, cookie)
	pt, _ := body["persistent_token"].(string)
	require.True(t, strings.HasPrefix(pt, models.PersistentTokenPrefix))

	return cookie, pt
}

func TestRecoverPassword_RevokesPersistentCredentialAndLoginSession(t *testing.T) {
	e, r := setupRecoveryAccount(t)
	cookie, pt := loginAccount(t, r, "qa", "correct-password")
	otherCookie, otherPT := loginAccount(t, r, "qa", "correct-password")

	bob := &models.User{Id: 2, UserName: "bob", EmailHash: "bob-hash", Status: models.UserStatusNormal}
	require.NoError(t, e.conn.Create(bob).Error)
	require.NoError(t, e.conn.Create(&models.Account{Id: 2, UserId: bob.Id, Username: "bob"}).Error)
	bobHash, err := models.EncryptPassword("bob-password", "salt2")
	require.NoError(t, err)
	require.NoError(t, e.conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.UserSecretData{UserId: bob.Id, Password: bobHash, Salt: "salt2"}).Error)
	_, bobPT := loginAccount(t, r, "bob", "bob-password")

	// Application tokens are not account login sessions. Admin password reset
	// revokes every auth token; this recovery path does not.
	oauthRow, _ := models.NewAuthToken(e.u.Id, models.AuthTokenTypeOauth, 9, 0, nil, 0, config.C.EncryptedKey)
	require.NoError(t, e.conn.Create(oauthRow).Error)

	require.NoError(t, e.conn.Model(&models.UserSecretData{}).Where("user_id = ?", e.u.Id).Update("password_scheme", "bcrypt").Error)

	token := issueRecoveryToken(t, e.conn, e.u.Id, time.Now().Add(5*time.Minute))
	w := callRecover(r, token, "recovered-password-123")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	oldRefresh, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": pt}, nil, nil)
	require.Equal(t, http.StatusUnauthorized, oldRefresh.Code)
	otherRefresh, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": otherPT}, nil, nil)
	require.Equal(t, http.StatusUnauthorized, otherRefresh.Code)

	oldSession, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{cookie}, nil)
	require.Equal(t, http.StatusUnauthorized, oldSession.Code)
	otherSession, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{otherCookie}, nil)
	require.Equal(t, http.StatusUnauthorized, otherSession.Code)
	require.Zero(t, liveAuthCount(t, e.conn, e.u.Id, models.AuthTokenTypeWeb, models.AuthTokenTypePersistent))

	bobRefresh, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": bobPT}, nil, nil)
	require.Equal(t, http.StatusOK, bobRefresh.Code)
	var bobSecret models.UserSecretData
	require.NoError(t, e.conn.Where("user_id = ?", bob.Id).First(&bobSecret).Error)
	require.NoError(t, bobSecret.CheckPassword("bob-password"))

	var oauth models.AuthToken
	require.NoError(t, e.conn.Where("token = ?", oauthRow.Token).First(&oauth).Error)
	require.Nil(t, oauth.Revoke)

	var secret models.UserSecretData
	require.NoError(t, e.conn.Where("user_id = ?", e.u.Id).First(&secret).Error)
	require.NoError(t, secret.CheckPassword("recovered-password-123"))
	require.Error(t, secret.CheckPassword("correct-password"))
	require.Empty(t, secret.PasswordScheme)

	freshCookie, freshPT := loginAccount(t, r, "qa", "recovered-password-123")
	freshSession, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{freshCookie}, nil)
	require.Equal(t, http.StatusOK, freshSession.Code)
	freshRefresh, body := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": freshPT}, nil, nil)
	require.Equal(t, http.StatusOK, freshRefresh.Code)
	require.NotEmpty(t, body["persistent_token"])

	again := callRecover(r, token, "another-password-123")
	require.Equal(t, http.StatusNotFound, again.Code)
	require.NoError(t, e.conn.Where("user_id = ?", e.u.Id).First(&secret).Error)
	require.NoError(t, secret.CheckPassword("recovered-password-123"))
}

func TestRecoverPassword_RotatedReplayCannotReissue(t *testing.T) {
	e, r := setupRecoveryAccount(t)
	_, pt1 := loginAccount(t, r, "qa", "correct-password")
	rotated, body := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": pt1}, nil, nil)
	require.Equal(t, http.StatusOK, rotated.Code)
	pt2, _ := body["persistent_token"].(string)
	require.NotEqual(t, pt1, pt2)

	var before int64
	require.NoError(t, e.conn.Model(&models.AuthToken{}).Where("user_id = ? AND type = ?", e.u.Id, models.AuthTokenTypePersistent).Count(&before).Error)

	token := issueRecoveryToken(t, e.conn, e.u.Id, time.Now().Add(5*time.Minute))
	require.Equal(t, http.StatusOK, callRecover(r, token, "recovered-password-123").Code)

	replay, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": pt1}, nil, nil)
	require.Equal(t, http.StatusUnauthorized, replay.Code)
	successor, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": pt2}, nil, nil)
	require.Equal(t, http.StatusUnauthorized, successor.Code)

	var after int64
	require.NoError(t, e.conn.Model(&models.AuthToken{}).Where("user_id = ? AND type = ?", e.u.Id, models.AuthTokenTypePersistent).Count(&after).Error)
	require.Equal(t, before, after, "replay after recovery must not mint a replacement credential")
	require.Zero(t, liveAuthCount(t, e.conn, e.u.Id, models.AuthTokenTypeWeb, models.AuthTokenTypePersistent))
}

func TestRecoverPassword_RejectedTokenLeavesCredentials(t *testing.T) {
	e, r := setupRecoveryAccount(t)
	_, pt := loginAccount(t, r, "qa", "correct-password")
	valid := issueRecoveryToken(t, e.conn, e.u.Id, time.Now().Add(5*time.Minute))

	wrong := strings.Repeat("c", 32) + "-1-" + uuid.NewString()
	require.Equal(t, http.StatusNotFound, callRecover(r, wrong, "recovered-password-123").Code)
	require.Equal(t, http.StatusNotFound, callRecover(r, "not-a-token", "recovered-password-123").Code)

	expired := issueRecoveryToken(t, e.conn, e.u.Id, time.Now().Add(-time.Minute))
	require.Equal(t, http.StatusNotFound, callRecover(r, expired, "recovered-password-123").Code)

	// Restore a usable token after the expiry case overwrote it, still unused.
	require.NoError(t, models.SaveUserMeta(e.conn, e.u.Id, "recover_password_verify_email_token", RecoverPasswordVerifyEmailToken{
		Token:    valid,
		ExpireAt: time.Now().Add(5 * time.Minute).Unix(),
	}))

	var secret models.UserSecretData
	require.NoError(t, e.conn.Where("user_id = ?", e.u.Id).First(&secret).Error)
	require.NoError(t, secret.CheckPassword("correct-password"))
	require.Error(t, secret.CheckPassword("recovered-password-123"))
	refresh, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": pt}, nil, nil)
	require.Equal(t, http.StatusOK, refresh.Code)
}

func TestRecoverPassword_PasswordUpdateFailureRollsBack(t *testing.T) {
	e, r := setupRecoveryAccount(t)
	_, pt := loginAccount(t, r, "qa", "correct-password")
	token := issueRecoveryToken(t, e.conn, e.u.Id, time.Now().Add(5*time.Minute))

	const hook = "test:fail_recovery_password_update"
	require.NoError(t, e.conn.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "user_secret_data" {
			_ = tx.AddError(errors.New("injected password update failure"))
		}
	}))
	failed := callRecover(r, token, "recovered-password-123")
	require.Equal(t, http.StatusInternalServerError, failed.Code, failed.Body.String())
	require.NoError(t, e.conn.Callback().Update().Remove(hook))

	var secret models.UserSecretData
	require.NoError(t, e.conn.Where("user_id = ?", e.u.Id).First(&secret).Error)
	require.NoError(t, secret.CheckPassword("correct-password"))
	require.Error(t, secret.CheckPassword("recovered-password-123"))
	require.NotZero(t, liveAuthCount(t, e.conn, e.u.Id, models.AuthTokenTypePersistent))

	require.Equal(t, http.StatusOK, callRecover(r, token, "recovered-password-123").Code)
	retry, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": pt}, nil, nil)
	require.Equal(t, http.StatusUnauthorized, retry.Code)
	require.NoError(t, e.conn.Where("user_id = ?", e.u.Id).First(&secret).Error)
	require.NoError(t, secret.CheckPassword("recovered-password-123"))
}

func TestRecoverPassword_RefreshDuringRevocationCannotEscape(t *testing.T) {
	e := newTwoFactorEnvDSN(t, filepath.Join(t.TempDir(), "recovery-race.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(50)")
	sqlDB, err := e.conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(4)
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "recovery-race-secret-key-32bytes!"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })
	r := newRecoveryRouter()
	_, pt := loginAccount(t, r, "qa", "correct-password")
	token := issueRecoveryToken(t, e.conn, e.u.Id, time.Now().Add(5*time.Minute))

	attempted := false
	refreshStatus := 0
	const hook = "test:refresh_during_recovery_snapshot"
	require.NoError(t, e.conn.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
		sql := tx.Statement.SQL.String()
		if attempted || !strings.Contains(sql, "auth_tokens") || !strings.Contains(sql, "revoke") {
			return
		}
		if _, ok := tx.Statement.Dest.(*[]string); !ok {
			return
		}
		attempted = true
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/session/refresh", strings.NewReader(`{"persistent_token":"`+pt+`"}`)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		refreshStatus = w.Code
	}))
	t.Cleanup(func() { _ = e.conn.Callback().Query().Remove(hook) })

	recovered := callRecover(r, token, "recovered-password-123")
	require.Equal(t, http.StatusOK, recovered.Code, recovered.Body.String())
	require.True(t, attempted, "recovery must snapshot credentials while holding the user lock")
	require.NotEqual(t, http.StatusOK, refreshStatus, "refresh cannot commit inside password recovery")
	require.Zero(t, liveAuthCount(t, e.conn, e.u.Id, models.AuthTokenTypeWeb, models.AuthTokenTypePersistent))
	retry, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": pt}, nil, nil)
	require.Equal(t, http.StatusUnauthorized, retry.Code)

	freshCookie, freshPT := loginAccount(t, r, "qa", "recovered-password-123")
	freshSession, _ := performJSONRequest(r, http.MethodGet, "/api/v1/accounts/verify_credentials", nil, []*http.Cookie{freshCookie}, nil)
	require.Equal(t, http.StatusOK, freshSession.Code)
	freshRefresh, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": freshPT}, nil, nil)
	require.Equal(t, http.StatusOK, freshRefresh.Code)
}
