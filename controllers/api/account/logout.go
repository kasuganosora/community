package account

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func Logout(c *gin.Context) {
	_, exists := c.Get("user")
	if !exists {
		c.JSON(401, gin.H{"error": "Unauthorized"}) //nolint:goconst

		return
	}

	// 删除token
	tokenStr := c.GetHeader("Authorization")

	if tokenStr == "" {
		tokenStr, _ = c.Cookie("app-token")
	}

	if tokenStr == "" {
		c.JSON(401, gin.H{"error": "Unauthorized"})

		return
	}

	// 如果前缀是 Bearer ，去掉前缀
	if len(tokenStr) >= 7 && tokenStr[0:7] == "Bearer " {
		tokenStr = tokenStr[7:]
	}

	// 删除token
	token, err := models.GetTokenByTokenStr(tokenStr)
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(500, gin.H{"error": "Internal Server Error"})

			err = errors.Wrap(err, "[Logout] GetTokenByTokenStr error")
			log.ErrorV(err)

			return
		}
	} else {
		err = db.GetDB().Delete(token).Error
		if err != nil {
			c.JSON(500, gin.H{"error": "Internal Server Error"})

			err = errors.Wrap(err, "[Logout] Delete token error")
			log.ErrorV(err)

			return
		}
	}

	// 删除cookie
	// Secure 标志根据请求是否为 HTTPS 动态判断
	// SameSite=Lax 与登录时设置 Cookie 保持一致，防止 CSRF
	secure := c.Request.TLS != nil
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie("app-token", "", -1, "/", "", secure, true)
	c.JSON(200, gin.H{"message": "ok"}) //nolint:goconst
}
