package middleware

import (
	"github.com/gin-gonic/gin"
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

		// An OAuth scope limits access; it never grants an ordinary user admin
		// privileges. Check the current authenticated user for both token types.
		for _, scope := range scopes {
			if scope.Type == models.AppScopeTypeAdmin {
				userRaw, _ := c.Get("user")
				user, ok := userRaw.(*models.User)
				if !ok || user == nil || !user.IsAdmin {
					returnUnauthorized(c)
					return
				}
			}
		}
		if tokenData.AuthType == models.AuthTokenTypeWeb {
			c.Next()
			return
		}

		// Oauth的都需要检查scope
		for _, scope := range scopes {
			if tokenData.Scopes == nil || !tokenData.Scopes.HasScope(scope.Type, scope.Value) {
				returnUnauthorized(c)

				return
			}
		}

		// 检查通过，继续执行
		c.Next()
	}
}

func returnUnauthorized(c *gin.Context) {
	c.JSON(401, gin.H{
		"code":    401, //nolint:goconst
		"message": "Unauthorized",
	})
	c.Abort()
}
