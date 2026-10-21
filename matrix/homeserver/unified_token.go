package homeserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	stderrors "errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// Unified tokens: a Mastodon API / OAuth access token carrying read:matrix
// and/or write:matrix may call the Matrix client-server API as its owner.
//
// Each such token is bound to one dedicated Matrix device whose device_id is
// derived from the token's DB key (SHA-256 of the raw token), so /sync
// positions, to-device messages and E2EE keys persist across requests. The
// device's session row is created lazily on first use and carries a random
// internal access token that is never handed out; the raw API token is never
// stored in Matrix tables.
//
// Logout via Matrix (/logout) with a unified token removes only the bound
// device session (it is re-created, empty, on the next request); the API
// token itself is never revoked from the Matrix side. Revoke it in settings.

const unifiedDevicePrefix = "MTOK"

// unifiedDeviceID derives the stable device_id for an API token hash.
func unifiedDeviceID(tokenHash string) string {
	sum := sha256.Sum256([]byte("community-unified-matrix-device:" + tokenHash))
	return unifiedDevicePrefix + strings.ToUpper(hex.EncodeToString(sum[:]))[:12]
}

// matrixReadOnlyPOST lists POST endpoints that only read (or only create
// client-side sync state), so read:matrix is enough for them.
func matrixReadOnlyPOST(path string) bool {
	return strings.HasSuffix(path, "/keys/query") ||
		strings.HasSuffix(path, "/user_directory/search") ||
		strings.HasSuffix(path, "/search") ||
		strings.HasSuffix(path, "/publicRooms") ||
		strings.HasSuffix(path, "/filter")
}

// matrixRequestIsRead reports whether the request needs only read:matrix.
func matrixRequestIsRead(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	case http.MethodPost:
		return matrixReadOnlyPOST(r.URL.Path)
	}
	return false
}

// unifiedTokenResolver is swapped in tests.
var unifiedTokenResolver = middleware.ResolveUnifiedToken

// resolveMatrixAccessToken resolves a client access token: native Matrix
// device tokens first (unchanged behaviour), then unified API tokens. The
// scope check runs here, so it applies equally to the Authorization header
// and the access_token query parameter. sessionToken is the access token of
// the resolved device session (internal for unified tokens).
func resolveMatrixAccessToken(r *http.Request, token string) (session *MatrixUserSession, status int, merr *MatrixError) {
	hs := GetHomeServer()
	if s, e := hs.Auth.GetDeviceByToken(token); e == nil {
		return s, 0, nil
	}
	// Native Matrix tokens have a recognisable prefix; never treat them as
	// API tokens.
	if token == "" || strings.HasPrefix(token, "syt_") || strings.HasPrefix(token, "mut_") {
		return nil, http.StatusUnauthorized, ErrUnknownToken
	}
	ut, err := unifiedTokenResolver(r.Context(), token)
	if err != nil {
		if stderrors.Is(err, middleware.ErrUnifiedTokenInvalid) {
			// Revoked / expired: drop the bound device if one exists.
			cleanupUnifiedDevice(middleware.UnifiedTokenHash(token))
		}
		return nil, http.StatusUnauthorized, ErrUnknownToken
	}
	other := ut.Scopes.OtherScope
	if other&(models.AppScopeReadMatrix|models.AppScopeWriteMatrix) == 0 {
		return nil, http.StatusUnauthorized, &MatrixError{ErrCode: "M_UNKNOWN_TOKEN", Error: "Token lacks the read:matrix / write:matrix permission"}
	}
	need := uint64(models.AppScopeWriteMatrix)
	if matrixRequestIsRead(r) {
		need = models.AppScopeReadMatrix
	}
	if other&need == 0 {
		missing := "write:matrix"
		if need == models.AppScopeReadMatrix {
			missing = "read:matrix"
		}
		return nil, http.StatusForbidden, &MatrixError{ErrCode: "M_FORBIDDEN", Error: "Token lacks the " + missing + " permission"}
	}
	s, e := bindUnifiedDevice(r.Context(), ut)
	if e != nil {
		return nil, http.StatusInternalServerError, ErrInternal
	}
	return s, 0, nil
}

// bindUnifiedDevice returns (creating lazily) the dedicated device session.
func bindUnifiedDevice(ctx context.Context, ut *middleware.UnifiedToken) (*MatrixUserSession, error) {
	userID := "@" + ut.Account.Username + ":" + GetHomeServer().Config.ServerName
	deviceID := unifiedDeviceID(ut.Hash)
	conn := db.GetDB().WithContext(ctx)
	var s MatrixUserSession
	res := conn.Where("user_id = ? AND device_id = ?", userID, deviceID).Limit(1).Find(&s)
	if res.Error != nil {
		return nil, res.Error
	}
	now := time.Now()
	if res.RowsAffected > 0 {
		if s.LastActiveTS == nil || now.UnixMilli()-*s.LastActiveTS > int64(time.Minute/time.Millisecond) {
			conn.Model(&s).Update("last_active_ts", now.UnixMilli())
		}
		return &s, nil
	}
	name := ut.AppName
	if name == "" {
		name = "API token"
	}
	accountID := ut.Account.Id
	s = MatrixUserSession{
		UserID:         userID,
		AccessToken:    "mut_" + strings.TrimPrefix(generateAccessToken(), "syt_"),
		DeviceID:       deviceID,
		DisplayName:    &name,
		LastActiveTS:   getUnixMilliPtr(now),
		LocalAccountID: &accountID,
		ExpiresAt:      ut.ExpiresAt,
	}
	if err := conn.Create(&s).Error; err != nil {
		// Concurrent first use: the other request created it.
		var again MatrixUserSession
		if conn.Where("user_id = ? AND device_id = ?", userID, deviceID).First(&again).Error == nil {
			return &again, nil
		}
		return nil, err
	}
	return &s, nil
}

// cleanupUnifiedDevice removes the bound device of a no-longer-valid token.
func cleanupUnifiedDevice(tokenHash string) {
	deviceID := unifiedDeviceID(tokenHash)
	_ = db.GetDB().Transaction(func(tx *gorm.DB) error {
		var s MatrixUserSession
		if r := tx.Where("device_id = ? AND access_token LIKE ?", deviceID, "mut_%").Limit(1).Find(&s); r.Error != nil || r.RowsAffected == 0 {
			return r.Error
		}
		if err := tx.Where("user_id = ? AND device_id = ?", s.UserID, s.DeviceID).Delete(&MatrixSyncToDeviceBatch{}).Error; err != nil {
			return err
		}
		return tx.Delete(&MatrixUserSession{}, s.ID).Error
	})
}

// setMatrixAuthContext stores the resolved session on the request.
func setMatrixAuthContext(c *gin.Context, s *MatrixUserSession) {
	c.Set("user_id", s.UserID)
	c.Set("device_id", s.DeviceID)
	c.Set("access_token", s.AccessToken)
}
