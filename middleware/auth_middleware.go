package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/secretredact"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RawAuthTokenKey is the gin context key holding the authenticated raw token.
const RawAuthTokenKey = "auth_token_raw"

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 尝试从 middleware header 中获取 token
		authToken := c.GetHeader("Authorization")
		if authToken == "" {
			// Mastodon clients authenticate streaming connections with the
			// access_token query parameter or the Sec-WebSocket-Protocol header,
			// because browsers cannot set Authorization on a WebSocket.
			authToken = streamingAccessToken(c)
		}
		if authToken == "" {
			// 尝试从Cookie中获取token
			authToken, _ = c.Cookie(AppTokenCookie)
			if authToken != "" {
				// Cookie-only auth from another site is CSRF / cross-site
				// WebSocket hijacking: refuse it. Bearer, access_token and
				// subprotocol auth keep working from any origin.
				if !cookieOriginAllowed(c.Request) {
					c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "cross-origin cookie authentication is not allowed"})
					return
				}
				c.Set(AuthViaCookieKey, true)
			}
		} else if c.GetHeader("Authorization") != "" {
			// 去掉前缀 Bearer
			const bearerPrefix = "Bearer "
			if !strings.HasPrefix(authToken, bearerPrefix) {
				returnUnauthorizedAndRemoveCookie(c)

				return
			}

			authToken = strings.TrimPrefix(authToken, bearerPrefix)
		}

		// 如果token为空，直接跳过 middleware, 交给后面的路由处理,如果需要检查登陆状态的路由，则需要多加一个 CheckAuthMiddleware 中间件
		if authToken == "" {
			c.Next()

			return
		}

		// 验证token
		tokenData, err := models.GetAuthTokenData(authToken, config.C.EncryptedKey)
		if err != nil {
			log.Error("GetAuthTokenData error: %s", secretredact.Scrub(c, fmt.Sprintf("%+v", err)))
			returnUnauthorizedAndRemoveCookie(c)

			return
		}

		// 判断token是否过期
		if tokenDataExpired(tokenData, time.Now()) {
			returnUnauthorizedAndRemoveCookie(c)

			return
		}

		// 每次都验证 token 是否有效（已撤销或已过期）
		// 优先查 Redis 缓存，减少数据库压力；缓存未命中则查库并写入缓存
		token, err := getTokenWithCacheContext(c.Request.Context(), authToken)
		if err != nil {
			returnUnauthorizedAndRemoveCookie(c)

			return
		}

		// ExpiredAt 为 nil 表示永不过期（见 AuthToken 模型注释），需判空避免空指针 panic
		if token.Revoke != nil || (token.ExpiredAt != nil && token.ExpiredAt.Unix() < time.Now().Unix()) {
			returnUnauthorizedAndRemoveCookie(c)

			return
		}

		// Personal tokens expose only the two explicitly granted coarse scopes.
		// Enforce them even on legacy routes without a granular scope guard.
		if token.ExternalClient == models.PersonalTokenClient {
			if token.EffectiveScopes != nil {
				tokenData.Scopes = token.EffectiveScopes
			}
			reading := c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions
			s := tokenData.Scopes
			if s == nil || (reading && s.ReadScope == 0 && s.AdminScope&models.AppScopeAdminRead == 0) || (!reading && !oauthTokenMayWrite(c.Request, tokenData)) {
				returnInsufficientScope(c)
				return
			}
		} else if !oauthTokenMayWrite(c.Request, tokenData) {
			returnInsufficientScope(c)
			return
		}
		if !granularWriteAllowed(c.Request, tokenData) {
			returnInsufficientScope(c)
			return
		}

		// 获取用户信息
		user := &models.User{}

		err = db.GetDB().WithContext(c.Request.Context()).Where("id = ?", tokenData.UserId).First(user).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				returnUnauthorizedAndRemoveCookie(c)

				return
			}

			err = errors.Wrap(err, fmt.Sprintf("[AuthMiddleware] get user error, user id: %d", tokenData.UserId))
			_ = c.Error(err)

			return
		}

		account, err := user.GetAccountBase(db.GetDB().WithContext(c.Request.Context()))
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				returnUnauthorizedAndRemoveCookie(c)

				return
			}

			err = errors.Wrap(err, fmt.Sprintf("[AuthMiddleware] get account error, user id: %d", tokenData.UserId))
			_ = c.Error(err)

			return
		}

		// Moderation is evaluated against current rows on every request,
		// including OAuth tokens already present in the token cache.
		if user.IsBan || user.Status != models.UserStatusNormal || account.IsBan {
			returnUnauthorizedAndRemoveCookie(c)
			return
		}
		if tokenData.ApplicationId > 0 {
			application := &models.Application{}

			err = db.GetDB().WithContext(c.Request.Context()).Where("id = ?", tokenData.ApplicationId).First(application).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					returnUnauthorizedAndRemoveCookie(c)

					return
				}

				err = errors.Wrap(err, fmt.Sprintf("[AuthMiddleware] get application error, application id: %d", tokenData.ApplicationId))
				_ = c.Error(err)

				return
			}

			c.Set("application", application)
		}

		// 将 tokenData 保存到 gin.Context 中
		c.Set("token_data", tokenData)
		// The raw token as actually presented (header, cookie, or streaming
		// query/subprotocol) so long-lived streams can revalidate it.
		c.Set(RawAuthTokenKey, authToken)
		c.Set("user", user)
		c.Set("account", account)
		// Optional-auth routes (search, account statuses, relationships, ...)
		// only run AuthMiddleware and check GetBool("authenticated"), which
		// was previously set by CheckAuthMiddleware alone.
		c.Set("authenticated", true)
		touchTokenLastUsed(c.Request.Context(), token, time.Now())
		touchAccountActive(c.Request.Context(), account, time.Now())
		c.Next()
	}
}

// tokenDataExpired reports whether the token payload is past its expiry.
// ExpiredAt == 0 means the token never expires (all OAuth tokens are issued
// that way), so it must not be treated as expired.
func tokenDataExpired(td *models.AuthTokenData, now time.Time) bool {
	return td.ExpiredAt > 0 && td.ExpiredAt < now.Unix()
}

func returnUnauthorizedAndRemoveCookie(c *gin.Context) {
	// 如果有 app-token cookie，删除
	// Secure 标志根据请求是否为 HTTPS 动态判断
	if _, err := c.Cookie(AppTokenCookie); err == nil {
		SetAppTokenCookie(c, "", -1)
	}

	c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"}) //nolint:goconst
	c.Abort()
}

// tokenCacheValidPersonal marks a cached valid personal access token.
const tokenCacheValidPersonal = "valid:personal"

// tokenCacheTTL token 缓存的 TTL（30 秒）.
const tokenCacheTTL = 30 * time.Second

// revokedTokenCacheTTL is how long a revoked/deleted token stays marked as
// revoked in Redis. After that the database (row deleted or revoke set) keeps
// rejecting it.
const revokedTokenCacheTTL = 10 * time.Minute

// tokenCacheKey returns the Redis key for a bearer token. The key uses the
// token's SHA-256 (the same digest stored in auth_tokens.token) so raw bearer
// tokens are never written to Redis.
func tokenCacheKey(tokenStr string) string {
	sum := sha256.Sum256([]byte(tokenStr))

	return "token_cache:" + hex.EncodeToString(sum[:])
}

// InvalidateTokenCache marks a token as revoked in the auth cache so that
// AuthMiddleware rejects it immediately after logout or OAuth revoke,
// instead of trusting a cached "valid" entry for up to tokenCacheTTL.
func InvalidateTokenCache(tokenStr string) {
	if tokenStr == "" {
		return
	}

	sum := sha256.Sum256([]byte(tokenStr))
	InvalidateTokenCacheHash(hex.EncodeToString(sum[:]))
}

// InvalidateTokenCacheHash marks a stored token digest as revoked.
// hash is the SHA-256 hex already stored in auth_tokens.token, used when the
// raw bearer token is no longer available (refresh rotation, code replay).
func InvalidateTokenCacheHash(hash string) {
	if hash == "" {
		return
	}

	redisClient := GetRedisClient()
	if redisClient == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := redisClient.Set(ctx, "token_cache:"+hash, "revoked", revokedTokenCacheTTL).Err(); err != nil {
		log.Warn("failed to invalidate token cache: %v", err)
	}
}

// tokenCacheBudget bounds optional Redis work across both the read and write.
// SQL authorization retains the original request context, not this cache budget.
const tokenCacheBudget = 200 * time.Millisecond

func getTokenWithCache(tokenStr string) (*models.AuthToken, error) {
	return getTokenWithCacheContext(context.Background(), tokenStr)
}

func getTokenWithCacheContext(requestContext context.Context, tokenStr string) (*models.AuthToken, error) {
	if err := requestContext.Err(); err != nil {
		return nil, err
	}
	cacheContext, cancel := context.WithTimeout(requestContext, tokenCacheBudget)
	defer cancel()
	redisClient := GetRedisClient()
	cacheKey := tokenCacheKey(tokenStr)
	canWriteCache, upgrade := false, false
	if redisClient != nil {
		cached, err := redisClient.Get(cacheContext, cacheKey).Result()
		if err == nil {
			if cached == "revoked" {
				return nil, errors.New("token revoked (cached)")
			}
			if cached == "valid" {
				return &models.AuthToken{}, nil
			}
			// Personal tokens are cached separately so the coarse
			// read/write scope check still applies on a cache hit.
			// Personal tokens cache their current (editable) scopes. The
			// old scope-less marker is treated as a miss and upgraded.
			if raw, ok := strings.CutPrefix(cached, tokenCacheValidPersonal+":"); ok {
				scopes := &models.ApplicationScope{}
				if json.Unmarshal([]byte(raw), scopes) == nil {
					return &models.AuthToken{ExternalClient: models.PersonalTokenClient, EffectiveScopes: scopes}, nil
				}
			}
			upgrade = cached == tokenCacheValidPersonal || strings.HasPrefix(cached, tokenCacheValidPersonal+":")
		}
		// A Redis outage is not a cache miss. Do not retry it after the SQL fallback.
		canWriteCache = err == nil || errors.Is(err, redis.Nil)
	}
	token := &models.AuthToken{}
	digest := strings.TrimPrefix(cacheKey, "token_cache:")
	if err := db.GetDB().WithContext(requestContext).Where("token = ?", digest).First(token).Error; err != nil {
		return nil, errors.Wrap(err, "get token by token str error")
	}
	if token.Revoke == nil && tokenInactive(token, time.Now()) {
		expireInactiveToken(requestContext, token)
		return nil, errors.New("token inactive")
	}
	if token.ExternalClient == models.PersonalTokenClient {
		var blob *models.ApplicationScope
		if data, err := models.GetAuthTokenData(tokenStr, config.C.EncryptedKey); err == nil {
			blob = data.Scopes
		}
		res, err := models.PersonalTokenEffectiveScopes(db.GetDB().WithContext(requestContext), digest, blob)
		if err != nil {
			return nil, errors.Wrap(err, "get personal token scopes error")
		}
		token.EffectiveScopes = res.Scopes
		if res.Mismatch != "" {
			warnScopeMismatchOnce(digest, "personal token %s user %d: scope mismatch %s", res.TokenID, token.UserId, res.Mismatch)
		}
	} else if token.ApplicationId != 0 {
		checkOAuthScopes(requestContext, tokenStr, digest, token)
	}
	if canWriteCache && cacheContext.Err() == nil {
		if token.Revoke != nil || (token.ExpiredAt != nil && token.ExpiredAt.Unix() > 0 && token.ExpiredAt.Unix() < time.Now().Unix()) {
			_ = redisClient.Set(cacheContext, cacheKey, "revoked", revokedTokenCacheTTL).Err()
		} else {
			// SetNX: never overwrite a "revoked" marker written by a concurrent
			// logout/revoke between our SQL read and this cache write.
			value := "valid"
			if token.ExternalClient == models.PersonalTokenClient {
				value = personalTokenCacheValue(token.EffectiveScopes)
			}
			if upgrade && value != "valid" {
				// Replace an old scope-less personal marker, but never a
				// revoke written meanwhile.
				_ = redisClient.SetXX(cacheContext, cacheKey, value, tokenCacheTTL).Err()
				if cur, _ := redisClient.Get(cacheContext, cacheKey).Result(); cur == "revoked" {
					return nil, errors.New("token revoked (cached)")
				}
			} else {
				_ = redisClient.SetNX(cacheContext, cacheKey, value, tokenCacheTTL).Err()
			}
		}
	}
	return token, nil
}

// SetTokenCacheRedisForTest swaps the Redis client used by the auth token
// cache and returns a function restoring the previous one. Tests only.
func SetTokenCacheRedisForTest(client *redis.Client) func() {
	globalRedisClientOnce.Do(func() {})
	old := globalRedisClient
	globalRedisClient = client

	return func() { globalRedisClient = old }
}

// streamingAccessToken returns the token a streaming request carries outside
// the Authorization header. It is accepted only on streaming endpoints so
// tokens are not encouraged in URLs of ordinary API requests.
func streamingAccessToken(c *gin.Context) string {
	if !isStreamingPath(c.Request.URL.Path) {
		return ""
	}
	// Tokens are standard base64; a client that forgets to URL-encode the
	// query turns '+' into a space, which can never be part of a token.
	if token := strings.TrimSpace(strings.ReplaceAll(c.Query("access_token"), " ", "+")); token != "" {
		return token
	}
	if websocket := strings.EqualFold(c.GetHeader("Upgrade"), "websocket"); websocket {
		protocols := strings.Split(c.GetHeader("Sec-WebSocket-Protocol"), ",")
		// Browser clients offer ["community.v1", "community.bearer.<base64url
		// token>"]: standard-base64 tokens contain '+' and '/', which are not
		// valid subprotocol characters, and the server echoes only the first
		// protocol, so the token is not reflected in the response.
		for _, p := range protocols {
			if token, ok := secretredact.DecodeBearerSubprotocol(strings.TrimSpace(p)); ok {
				return token
			}
		}
		return strings.TrimSpace(protocols[0])
	}
	return ""
}

func isStreamingPath(path string) bool {
	for _, prefix := range []string{"/api/v1/streaming", "/api/v2/streaming"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// oauthTokenMayWrite is the coarse guard for OAuth tokens on state-changing
// requests: a token granted none of write, admin:write, follow or push
// (a read-only token) gets 403 on every write route, including routes that
// carry no granular CheckScopeMiddleware. Granular route guards still apply.
func oauthTokenMayWrite(r *http.Request, td *models.AuthTokenData) bool {
	if td.AuthType != models.AuthTokenTypeOauth {
		return true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	// Mastodon grants translation with read:statuses even though it is a POST.
	if isTranslatePath(r.URL.Path) {
		return true
	}
	s := td.Scopes
	return s != nil && (s.WriteScope != 0 || s.AdminScope&models.AppScopeAdminWrite != 0 || s.OtherScope&(models.AppScopeFollow|models.AppScopePush) != 0)
}

// tokenLastUsedInterval throttles last_used_at writes per token.
const tokenLastUsedInterval = time.Hour

// tokenInactive applies the optional inactivity rule to API tokens: idle
// time is measured from the later of created_at and last_used_at.
func tokenInactive(token *models.AuthToken, now time.Time) bool {
	if token.Type == models.AuthTokenTypeWeb || !systemsettings.APITokenInactivityExpiry() {
		return false
	}
	var since time.Time
	if token.CreatedAt != nil {
		since = *token.CreatedAt
	}
	if token.LastUsedAt != nil && token.LastUsedAt.After(since) {
		since = *token.LastUsedAt
	}
	return !since.IsZero() && now.Sub(since) > systemsettings.APITokenInactivityLimit
}

// expireInactiveToken soft-revokes an inactive token (the row is kept) so it
// stays invalid even if the rule is later turned off. The conditional UPDATE
// is idempotent under concurrent requests.
func expireInactiveToken(ctx context.Context, token *models.AuthToken) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	err := db.GetDB().WithContext(ctx).Model(&models.AuthToken{}).
		Where("token = ? AND revoke IS NULL", token.Token).
		Updates(map[string]any{"revoke": time.Now(), "revoke_reason": models.RevokeReasonInactive}).Error
	if err != nil {
		log.Warn("expire inactive token: %v", err)
	}
	InvalidateTokenCacheHash(token.Token)
}

// touchTokenLastUsed records use of an authenticated token. It runs only when
// the row was read from the database (cache hits carry no row) and the stored
// value is older than tokenLastUsedInterval, so at most one small UPDATE per
// token per hour. Failures are ignored: this must never fail the request.
func touchTokenLastUsed(ctx context.Context, token *models.AuthToken, now time.Time) {
	if token == nil || token.Token == "" || (token.LastUsedAt != nil && now.Sub(*token.LastUsedAt) < tokenLastUsedInterval) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	_ = db.GetDB().WithContext(ctx).Model(&models.AuthToken{}).
		Where("token = ? AND (last_used_at IS NULL OR last_used_at < ?)", token.Token, now.Add(-tokenLastUsedInterval)).
		Update("last_used_at", now).Error
}

func isTranslatePath(path string) bool {
	return strings.HasPrefix(path, "/api/v1/statuses/") && strings.HasSuffix(path, "/translate")
}

// accountActiveInterval throttles accounts.last_active_at writes.
const accountActiveInterval = 5 * time.Minute

// touchAccountActive records Misskey's lastActiveDate for a local account
// at most every accountActiveInterval. Failures never fail the request.
func touchAccountActive(ctx context.Context, account *models.Account, now time.Time) {
	if account == nil || account.Id == 0 || account.Domain != nil || (account.LastActiveAt != nil && now.Sub(*account.LastActiveAt) < accountActiveInterval) {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 500*time.Millisecond)
	defer cancel()
	_ = db.GetDB().WithContext(ctx).Model(&models.Account{}).
		Where("id = ? AND (last_active_at IS NULL OR last_active_at < ?)", account.Id, now.Add(-accountActiveInterval)).
		UpdateColumn("last_active_at", now).Error
	account.LastActiveAt = &now
}

// personalTokenCacheValue encodes a valid personal token with its current
// scopes; without parsed scopes it stores the plain marker (blob scopes).
func personalTokenCacheValue(scopes *models.ApplicationScope) string {
	if scopes == nil {
		return tokenCacheValidPersonal
	}
	raw, err := json.Marshal(scopes)
	if err != nil {
		return tokenCacheValidPersonal
	}
	return tokenCacheValidPersonal + ":" + string(raw)
}

// RefreshPersonalTokenCache stores a personal token's new scopes in the auth
// cache right after an edit so the next request uses them. An existing
// "revoked" marker is kept.
func RefreshPersonalTokenCache(hash string, scopes *models.ApplicationScope) {
	redisClient := GetRedisClient()
	if hash == "" || redisClient == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	key := "token_cache:" + hash
	if cur, err := redisClient.Get(ctx, key).Result(); err == nil && cur == "revoked" {
		return
	}
	if err := redisClient.Set(ctx, key, personalTokenCacheValue(scopes), tokenCacheTTL).Err(); err != nil {
		// Fall back to dropping the entry; the next request reads SQL.
		_ = redisClient.Del(ctx, key).Err()
		log.Warn("failed to refresh personal token cache: %v", err)
	}
}

// scopeMismatchWarned dedupes scope mismatch warnings per token digest for
// the life of the process.
var scopeMismatchWarned sync.Map

func warnScopeMismatchOnce(digest, format string, args ...any) {
	if _, seen := scopeMismatchWarned.LoadOrStore(digest, struct{}{}); !seen {
		log.Warn(format, args...)
	}
}

// checkOAuthScopes compares an OAuth / app token's blob scopes with the
// scopes its application registered and logs, once, a token holding bits the
// application does not have. The blob stays authoritative for OAuth and web
// session tokens (they have no per-token DB scopes); nothing is changed.
func checkOAuthScopes(ctx context.Context, tokenStr, digest string, token *models.AuthToken) {
	if _, seen := scopeMismatchWarned.Load(digest); seen {
		return
	}
	data, err := models.GetAuthTokenData(tokenStr, config.C.EncryptedKey)
	if err != nil || data.Scopes == nil {
		return
	}
	var app models.Application
	if db.GetDB().WithContext(ctx).Select("id", "name", "scopes").Where("id = ?", token.ApplicationId).Limit(1).Find(&app).Error != nil || app.Id == 0 {
		return
	}
	s, a := data.Scopes, app.Scopes
	if s.ReadScope&^a.ReadScope != 0 || s.WriteScope&^a.WriteScope != 0 || s.AdminScope&^a.AdminScope != 0 || s.OtherScope&^a.OtherScope != 0 {
		warnScopeMismatchOnce(digest, "oauth token %.12s user %d app %d (%s): token scopes {read=%#x write=%#x admin=%#x other=%#x} exceed app scopes {read=%#x write=%#x admin=%#x other=%#x}; token scopes kept",
			digest, token.UserId, app.Id, app.Name, s.ReadScope, s.WriteScope, s.AdminScope, s.OtherScope, a.ReadScope, a.WriteScope, a.AdminScope, a.OtherScope)
	}
}
