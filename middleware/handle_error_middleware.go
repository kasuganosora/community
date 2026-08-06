package middleware

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
)

func HandleErrorsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		lastError := c.Errors.Last()
		if lastError == nil {
			return
		}

		ret := gin.H{
			"error": "internal server error",
		}

		if config.C.DevMode == "true" {
			ret["error"] = lastError.Error()
			ret["stack"] = fmt.Sprintf("%+v", lastError.Err)
		}

		// 使用结构化日志记录错误，而非 fmt.Printf
		log.Error("Request error: %+v", lastError.Err)

		if c.Writer.Written() {
			return
		}

		c.AbortWithStatusJSON(500, ret)
	}
}
