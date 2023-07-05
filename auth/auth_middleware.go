package auth

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 尝试从 auth header 中获取 token
		authToken := c.GetHeader("Authorization")
		if authToken == "" {
			// 尝试从Cookie中获取token
			authToken, _ = c.Cookie("app-token")
		}

		// 如果token为空，返回401
		if authToken == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}

	}
}
