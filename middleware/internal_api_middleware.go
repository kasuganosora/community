package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
)

func InternalApiMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.MustGet("token_data").(*models.AuthTokenData)

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
