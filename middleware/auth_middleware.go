package middleware

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/secretredact"
	"github.com/pkg/errors"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"net/http"
	"strings"
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
			authToken, _ = c.Cookie("app-token")
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
			reading := c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions
			if tokenData.Scopes == nil || (reading && tokenData.Scopes.ReadScope == 0) || (!reading && tokenData.Scopes.WriteScope == 0) {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient_scope"})
				return
			}
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
	secure := c.Request.TLS != nil
	if _, err := c.Cookie("app-token"); err == nil {
		// SameSite=Lax 与登录时设置 Cookie 保持一致，防止 CSRF
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie("app-token", "", -1, "/", "", secure, true)
	}

	c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"}) //nolint:goconst
	c.Abort()
}

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
	canWriteCache := false
	if redisClient != nil {
		cached, err := redisClient.Get(cacheContext, cacheKey).Result()
		if err == nil {
			if cached == "revoked" {
				return nil, errors.New("token revoked (cached)")
			}
			if cached == "valid" {
				return &models.AuthToken{}, nil
			}
		}
		// A Redis outage is not a cache miss. Do not retry it after the SQL fallback.
		canWriteCache = err == nil || errors.Is(err, redis.Nil)
	}
	token := &models.AuthToken{}
	digest := strings.TrimPrefix(cacheKey, "token_cache:")
	if err := db.GetDB().WithContext(requestContext).Where("token = ?", digest).First(token).Error; err != nil {
		return nil, errors.Wrap(err, "get token by token str error")
	}
	if canWriteCache && cacheContext.Err() == nil {
		if token.Revoke != nil || (token.ExpiredAt != nil && token.ExpiredAt.Unix() > 0 && token.ExpiredAt.Unix() < time.Now().Unix()) {
			_ = redisClient.Set(cacheContext, cacheKey, "revoked", revokedTokenCacheTTL).Err()
		} else {
			// SetNX: never overwrite a "revoked" marker written by a concurrent
			// logout/revoke between our SQL read and this cache write.
			_ = redisClient.SetNX(cacheContext, cacheKey, "valid", tokenCacheTTL).Err()
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
