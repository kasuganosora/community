package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

func GetMe(c *gin.Context) {
	user, exists := c.Get("user")
	if !exists {
		c.JSON(401, gin.H{"error": "Unauthorized"})
		return
	}

	account, err := user.(*models.User).GetAccount(db.GetDB())
	if err != nil {
		c.JSON(500, gin.H{"error": "Internal Server Error"})
		return
	}

}
