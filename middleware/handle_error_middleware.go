package middleware

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
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

		fmt.Printf("%+v", lastError.Err)

		c.AbortWithStatusJSON(500, ret)
	}
}
