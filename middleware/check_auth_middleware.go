package middleware

import "github.com/gin-gonic/gin"

func CheckAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, exists := c.Get("tokenData"); !exists {
			returnUnauthorized(c)
			return
		}

		c.Set("authenticated", true)
		c.Next()
	}
}
