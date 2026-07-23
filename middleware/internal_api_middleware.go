package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
)

func InternalApiMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenRaw, exists := c.Get("token_data")
		if !exists {
			c.AbortWithStatusJSON(401, gin.H{"code": 401, "msg": "unauthorized"}) //nolint:goconst

			return
		}

		token, ok := tokenRaw.(*models.AuthTokenData)
		if !ok {
			c.AbortWithStatusJSON(401, gin.H{"code": 401, "msg": "unauthorized"})

			return
		}

		if token.AuthType != models.AuthTokenTypeWeb {
			c.AbortWithStatusJSON(403, gin.H{
				"code": 403,
				"msg":  "invalid token",
			})

			return
		}

		c.Next()
	}
}
