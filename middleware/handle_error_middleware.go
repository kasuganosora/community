package middleware

import (
	"context"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/secretredact"
)

func HandleErrorsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		lastError := c.Errors.Last()
		if lastError == nil {
			return
		}

		if errors.Is(lastError.Err, context.Canceled) && c.Request != nil && errors.Is(c.Request.Context().Err(), context.Canceled) {
			log.Debug("Client disconnected: %s", secretredact.Scrub(c, lastError.Err.Error()))
			if !c.Writer.Written() {
				c.AbortWithStatus(StatusClientClosedRequest)
			}
			return
		}

		ret := gin.H{
			"error": "internal server error",
		}

		if config.C.DevMode == "true" {
			ret["error"] = secretredact.Scrub(c, lastError.Error())
			ret["stack"] = secretredact.Scrub(c, fmt.Sprintf("%+v", lastError.Err))
		}

		// 使用结构化日志记录错误，而非 fmt.Printf
		log.Error("Request error: %s", secretredact.Scrub(c, fmt.Sprintf("%+v", lastError.Err)))

		if c.Writer.Written() {
			return
		}

		c.AbortWithStatusJSON(500, ret)
	}
}
