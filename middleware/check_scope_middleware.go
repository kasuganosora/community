package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

type Scope struct {
	Type  uint8
	Value uint64
}

// CheckScopeMiddleware 检查用户是否有权限访问某个资源
// 必须全部满足才能访问.
func CheckScopeMiddleware(scopes ...Scope) gin.HandlerFunc {
	return func(c *gin.Context) {
		td, exists := c.Get("token_data")
		// 如果token不存在，返回401
		if !exists {
			returnUnauthorized(c)

			return
		}

		tokenData, ok := td.(*models.AuthTokenData)
		if !ok || tokenData == nil {
			returnUnauthorized(c)

			return
		}

		hasAdminScope := false

		// An OAuth scope limits access; it never grants an ordinary user admin
		// privileges. Check the current authenticated user for both token types.
		for _, scope := range scopes {
			if scope.Type == models.AppScopeTypeAdmin {
				hasAdminScope = true
				userRaw, _ := c.Get("user")
				user, ok := userRaw.(*models.User)
				if !ok || user == nil || user.IsBan || user.Status != models.UserStatusNormal {
					returnUnauthorized(c)
					return
				}
				if !user.IsAdmin {
					required := adminRoutePermission(c.Request.URL.Path)
					if required == 0 {
						returnInsufficientScope(c)
						return
					}
					bits, err := models.EffectiveAdminPermissions(db.GetDB().WithContext(c.Request.Context()), user)
					if err != nil {
						c.AbortWithStatusJSON(500, gin.H{"error": "permission lookup failed"})
						return
					}
					if bits&required != required {
						returnInsufficientScope(c)
						return
					}
				}
			}
		}
		finish := func() {
			if hasAdminScope {
				c.Set("admin_authorized", true)
				AdminAuditMiddleware()(c)
			} else {
				c.Next()
			}
		}
		if tokenData.AuthType == models.AuthTokenTypeWeb {
			finish()
			return
		}

		// Oauth的都需要检查scope
		for _, scope := range scopes {
			if tokenData.Scopes == nil || !tokenData.Scopes.HasScope(scope.Type, scope.Value) {
				returnInsufficientScope(c)

				return
			}
		}

		// 检查通过，继续执行
		finish()
	}
}

// CheckScopeIfTokenMiddleware is Mastodon's authorize_if_got_token!: an
// anonymous request passes (the handler decides what it may see), while a
// request carrying a token must hold the scopes like CheckScopeMiddleware.
func CheckScopeIfTokenMiddleware(scopes ...Scope) gin.HandlerFunc {
	check := CheckScopeMiddleware(scopes...)
	return func(c *gin.Context) {
		if _, exists := c.Get("token_data"); !exists {
			c.Next()
			return
		}
		check(c)
	}
}

func returnUnauthorized(c *gin.Context) {
	c.JSON(401, gin.H{
		"code":    401, //nolint:goconst
		"message": "Unauthorized",
	})
	c.Abort()
}

// InsufficientScopeError is the Mastodon-compatible body for a valid token
// that lacks the scope or permission a route requires.
const InsufficientScopeError = "This action is outside the authorized scopes"

// returnInsufficientScope answers 403: the token is valid, so clients must
// not treat it as a logout signal the way they treat 401.
func returnInsufficientScope(c *gin.Context) {
	c.AbortWithStatusJSON(403, gin.H{"error": InsufficientScopeError, "error_code": "insufficient_scope"})
}
