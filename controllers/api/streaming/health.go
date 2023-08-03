package streaming

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
)

func Health(c *gin.Context) {
	ctx := c.Request.Context()
	err := db.GetRedis().Ping(ctx).Err()
	if err != nil {
		c.AbortWithStatus(500)
		return
	}

	c.Writer.Write([]byte("OK"))
}
