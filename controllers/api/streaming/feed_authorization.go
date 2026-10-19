package streaming

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/middleware"
	"github.com/kasuganosora/community/models"
)

// currentFeedStreamingAccount revalidates an already authenticated connection
// directly against SQL. No optional cache can extend a revoked stream's life.
// revalidateFeedStreamingAccount is swapped out only by tests whose embedded
// SQL engine cannot run the three-way join below.
var revalidateFeedStreamingAccount = currentFeedStreamingAccount

// SetFeedRevalidationForTest replaces the feed revalidation query and returns
// a restore func. Test-only.
func SetFeedRevalidationForTest(fn func(c *gin.Context, original *models.Account) (*models.Account, error)) func() {
	prev := revalidateFeedStreamingAccount
	revalidateFeedStreamingAccount = fn
	return func() { revalidateFeedStreamingAccount = prev }
}

func currentFeedStreamingAccount(c *gin.Context, original *models.Account) (*models.Account, error) {
	value, exists := c.Get("token_data")
	td, ok := value.(*models.AuthTokenData)
	if !exists || !ok || td == nil || original == nil || td.UserId == 0 {
		return nil, errEventNotVisible
	}
	now := time.Now()
	if td.ExpiredAt > 0 && td.ExpiredAt < now.Unix() {
		return nil, errEventNotVisible
	}
	// Revalidate exactly the token the middleware accepted, whichever way the
	// client presented it (Authorization, cookie, access_token, subprotocol).
	raw, _ := c.Get(middleware.RawAuthTokenKey)
	token, _ := raw.(string)
	if token == "" {
		token = c.GetHeader("Authorization")
		if token == "" {
			token, _ = c.Cookie("app-token")
		} else if !strings.HasPrefix(token, "Bearer ") {
			return nil, errEventNotVisible
		} else {
			token = strings.TrimPrefix(token, "Bearer ")
		}
	}
	if token == "" {
		return nil, errEventNotVisible
	}
	sum := sha256.Sum256([]byte(token))
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	var current models.Account
	err := db.GetDB().WithContext(ctx).Model(&models.Account{}).
		Joins("JOIN users ON users.id = accounts.user_id").
		Joins("JOIN auth_tokens ON auth_tokens.user_id = users.id").
		Where("accounts.id = ? AND users.id = ? AND auth_tokens.token = ?", original.Id, td.UserId, hex.EncodeToString(sum[:])).
		Where("users.deleted_at IS NULL AND users.is_ban = ? AND users.status = ? AND accounts.is_ban = ?", false, models.UserStatusNormal, false).
		Where("auth_tokens.revoke IS NULL AND (auth_tokens.expired_at IS NULL OR auth_tokens.expired_at >= ?)", now).
		First(&current).Error
	if err != nil {
		return nil, err
	}
	return &current, nil
}
