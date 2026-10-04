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
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"net/http"
	"strings"
	"time"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 尝试从 middleware header 中获取 token
		authToken := c.GetHeader("Authorization")
		if authToken == "" {
			// 尝试从Cookie中获取token
			authToken, _ = c.Cookie("app-token")
		} else {
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
			log.Error("GetAuthTokenData error: %+v", err)
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
		token, err := getTokenWithCache(authToken)
		if err != nil {
			returnUnauthorizedAndRemoveCookie(c)

			return
		}

		// ExpiredAt 为 nil 表示永不过期（见 AuthToken 模型注释），需判空避免空指针 panic
		if token.Revoke != nil || (token.ExpiredAt != nil && token.ExpiredAt.Unix() < time.Now().Unix()) {
			returnUnauthorizedAndRemoveCookie(c)

			return
		}

		// 获取用户信息
		user := &models.User{}

		err = db.GetDB().Where("id = ?", tokenData.UserId).First(user).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				returnUnauthorizedAndRemoveCookie(c)

				return
			}

			err = errors.Wrap(err, fmt.Sprintf("[AuthMiddleware] get user error, user id: %d", tokenData.UserId))
			_ = c.Error(err)

			return
		}

		account, err := user.GetAccountBase(db.GetDB())
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				returnUnauthorizedAndRemoveCookie(c)

				return
			}

			err = errors.Wrap(err, fmt.Sprintf("[AuthMiddleware] get account error, user id: %d", tokenData.UserId))
			_ = c.Error(err)

			return
		}

		if tokenData.ApplicationId > 0 {
			application := &models.Application{}

			err = db.GetDB().Where("id = ?", tokenData.ApplicationId).First(application).Error
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

// getTokenWithCache 优先从 Redis 缓存获取 token，缓存未命中则查库并写入缓存
// 这样在高并发场景下可以将数据库查询量降低 ~97%（30 秒内同一 token 只查一次库）.
func getTokenWithCache(tokenStr string) (*models.AuthToken, error) {
	// 尝试从 Redis 缓存获取
	redisClient := GetRedisClient()
	if redisClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		cacheKey := tokenCacheKey(tokenStr)

		cached, err := redisClient.Get(ctx, cacheKey).Result()
		if err == nil {
			if cached == "revoked" {
				return nil, errors.New("token revoked (cached)")
			}
			// 缓存命中且有效，返回一个最小化的 token 对象
			// 实际校验逻辑已通过 JWT 完成，这里只需要确认 token 未被撤销
			return &models.AuthToken{}, nil
		}
	}

	// 缓存未命中，查库
	token, err := models.GetTokenByTokenStr(tokenStr)
	if err != nil {
		return nil, err
	}

	// 写入 Redis 缓存
	if redisClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		cacheKey := tokenCacheKey(tokenStr)
		if token.Revoke != nil || (token.ExpiredAt != nil && token.ExpiredAt.Unix() > 0 && token.ExpiredAt.Unix() < time.Now().Unix()) {
			// 已撤销/过期的 token 缓存为 "revoked"，使用较长 TTL 减少查库
			_ = redisClient.Set(ctx, cacheKey, "revoked", revokedTokenCacheTTL).Err()
		} else {
			// 有效 token 缓存为 "valid"，短 TTL 保证撤销后 30 秒内失效
			_ = redisClient.Set(ctx, cacheKey, "valid", tokenCacheTTL).Err()
		}
	}

	return token, nil
}
