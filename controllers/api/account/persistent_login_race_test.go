package account

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Force the original vulnerable interleaving: revocation has collected hashes,
// then another connection tries to rotate before those hashes are revoked.
// With no shared transaction lock, rotation succeeds and its new pair survives.
func TestPersistentLogin_RevocationSerializesRotation(t *testing.T) {
	for _, reason := range []string{"", models.RevokeReasonReplay} {
		t.Run("reason="+reason, func(t *testing.T) {
			e := newTwoFactorEnvDSN(t, filepath.Join(t.TempDir(), "race.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(50)")
			sqlDB, err := e.conn.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(4) // Independent connections, not pool serialization.
			oldKey := config.C.EncryptedKey
			config.C.EncryptedKey = "persistent-race-test-secret-key!"
			t.Cleanup(func() { config.C.EncryptedKey = oldKey })
			r := newPersistentRouter()
			login, body := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/login", map[string]any{"username": "qa", "password": "correct-password"}, nil, nil)
			require.Equal(t, http.StatusOK, login.Code)
			pt := body["persistent_token"].(string)
			claims, err := models.ParsePersistentToken(pt, config.C.EncryptedKey)
			require.NoError(t, err)
			// A second device must not be included in the revocation.
			other, otherBody := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/login", map[string]any{"username": "qa", "password": "correct-password"}, nil, nil)
			require.Equal(t, http.StatusOK, other.Code)

			attempted := false
			rotationStatus := 0
			const hook = "test:rotate_after_family_snapshot"
			require.NoError(t, e.conn.Callback().Query().After("gorm:query").Register(hook, func(tx *gorm.DB) {
				if attempted || !strings.Contains(tx.Statement.SQL.String(), "external_client") {
					return
				}
				if _, ok := tx.Statement.Dest.(*[]string); !ok {
					return
				}
				attempted = true
				// No sleeps or scheduling guesses: invoke the competing request exactly
				// inside the snapshot/update gap. A deadline bounds its blocked write.
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/session/refresh", strings.NewReader(`{"persistent_token":"`+pt+`"}`)).WithContext(ctx)
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				rotationStatus = w.Code
			}))
			t.Cleanup(func() { _ = e.conn.Callback().Query().Remove(hook) })
			hashes, err := revokeDeviceCredentialFamily(e.conn, e.u.Id, models.PersistentDeviceClient(claims.DeviceId), reason, time.Now())
			require.NoError(t, err)
			require.True(t, attempted)
			require.NotEqual(t, http.StatusOK, rotationStatus, "rotation cannot commit inside family revocation")
			require.Len(t, hashes, 2)
			var live int64
			require.NoError(t, e.conn.Model(&models.AuthToken{}).Where("user_id = ? AND external_client = ? AND revoke IS NULL", e.u.Id, models.PersistentDeviceClient(claims.DeviceId)).Count(&live).Error)
			require.Zero(t, live, "no replacement credentials may escape revocation")
			retry, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": pt}, nil, nil)
			require.Equal(t, http.StatusUnauthorized, retry.Code)
			otherRefresh, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": otherBody["persistent_token"]}, nil, nil)
			require.Equal(t, http.StatusOK, otherRefresh.Code)
		})
	}
}

func TestPersistentLogin_LogoutReportsFamilyRevocationFailure(t *testing.T) {
	e := newTwoFactorEnv(t)
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "persistent-race-test-secret-key!"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })
	r := newPersistentRouter()
	login, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/login", map[string]any{"username": "qa", "password": "correct-password"}, nil, nil)
	require.Equal(t, http.StatusOK, login.Code)
	const hook = "test:fail_family_lock"
	require.NoError(t, e.conn.Callback().Update().Before("gorm:update").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errors.New("injected family lock failure"))
		}
	}))
	t.Cleanup(func() { _ = e.conn.Callback().Update().Remove(hook) })
	logout, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/logout", nil, []*http.Cookie{extractAppTokenCookie(login)}, nil)
	require.Equal(t, http.StatusInternalServerError, logout.Code)
}

func TestPersistentLogin_RevocationIncludesCommittedRotation(t *testing.T) {
	e := newTwoFactorEnv(t)
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "persistent-race-test-secret-key!"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })
	r := newPersistentRouter()
	login, body := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/login", map[string]any{"username": "qa", "password": "correct-password"}, nil, nil)
	require.Equal(t, http.StatusOK, login.Code)
	pt := body["persistent_token"].(string)
	claims, err := models.ParsePersistentToken(pt, config.C.EncryptedKey)
	require.NoError(t, err)
	rotated, next := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": pt}, nil, nil)
	require.Equal(t, http.StatusOK, rotated.Code)
	// Use the original device identity, not the newly returned token hashes.
	hashes, err := revokeDeviceCredentialFamily(e.conn, e.u.Id, models.PersistentDeviceClient(claims.DeviceId), "", time.Now())
	require.NoError(t, err)
	require.Contains(t, hashes, models.PersistentTokenHash(next["persistent_token"].(string)))
	retry, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/session/refresh", map[string]any{"persistent_token": next["persistent_token"]}, nil, nil)
	require.Equal(t, http.StatusUnauthorized, retry.Code)
}

func TestPersistentLogin_LogoutReportsFamilyLookupFailure(t *testing.T) {
	e := newTwoFactorEnv(t)
	oldKey := config.C.EncryptedKey
	config.C.EncryptedKey = "persistent-race-test-secret-key!"
	t.Cleanup(func() { config.C.EncryptedKey = oldKey })
	r := newPersistentRouter()
	login, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/login", map[string]any{"username": "qa", "password": "correct-password"}, nil, nil)
	require.Equal(t, http.StatusOK, login.Code)
	const hook = "test:fail_family_lookup"
	require.NoError(t, e.conn.Callback().Query().Before("gorm:query").Register(hook, func(tx *gorm.DB) {
		if tx.Statement.Table == "auth_tokens" && strings.Contains(strings.Join(tx.Statement.Selects, ","), "external_client") {
			tx.AddError(errors.New("injected family lookup failure"))
		}
	}))
	t.Cleanup(func() { _ = e.conn.Callback().Query().Remove(hook) })
	logout, _ := performJSONRequest(r, http.MethodPost, "/api/v1/accounts/logout", nil, []*http.Cookie{extractAppTokenCookie(login)}, nil)
	require.Equal(t, http.StatusInternalServerError, logout.Code)
}
