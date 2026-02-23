package middleware

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"math/rand"
	"net/http"
	"time"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 尝试从 middleware header 中获取 token
		authToken := c.GetHeader("Authorization")
		if authToken == "" {
			// 尝试从Cookie中获取token
			authToken, _ = c.Cookie("app-token")
		} else {
			// 去掉前缀 Bearer
			if len(authToken) < 7 || authToken[0:6] != "Bearer" {
				returnUnauthorizedAndRemoveCookie(c)
				return
			}

			authToken = authToken[7:]
		}

		// 如果token为空，直接跳过 middleware, 交给后面的路由处理,如果需要检查登陆状态的路由，则需要多加一个 CheckAuthMiddleware 中间件
		if authToken == "" {
			c.Next()
			return
		}

		// 验证token
		tokenData, err := models.GetAuthTokenData(authToken, config.C.EncryptedKey)
		if err != nil {
			log.Error("GetAuthTokenData error: %+v", err)
			returnUnauthorizedAndRemoveCookie(c)
			return
		}

		// 判断token是否过期
		if tokenData.ExpiredAt < time.Now().Unix() {
			returnUnauthorizedAndRemoveCookie(c)
			return
		}

		// 随机抽验 token 是否在数据库中
		randInt := rand.Int63() + tokenData.CreatedAt
		var token *models.AuthToken
		if randInt%2 == 0 {
			token, err = models.GetTokenByTokenStr(authToken)
			if err != nil {
				returnUnauthorizedAndRemoveCookie(c)
				return
			}

			if token.Revoke != nil || token.ExpiredAt.Unix() < time.Now().Unix() {
				returnUnauthorizedAndRemoveCookie(c)
				return
			}
		}

		// 获取用户信息
		user := &models.User{}
		err = db.GetDB().Where("id = ?", tokenData.UserId).First(user).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				returnUnauthorizedAndRemoveCookie(c)
			}

			err = errors.Wrap(err, fmt.Sprintf("[AuthMiddleware] get user error, user id: %d", tokenData.UserId))
			_ = c.Error(err)
			return
		}

		account, err := user.GetAccountBase(db.GetDB())

		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				returnUnauthorizedAndRemoveCookie(c)
			}

			err = errors.Wrap(err, fmt.Sprintf("[AuthMiddleware] get account error, user id: %d", tokenData.UserId))
			_ = c.Error(err)
			return
		}

		if tokenData.ApplicationId > 0 {
			application := &models.Application{}
			err = db.GetDB().Where("id = ?", tokenData.ApplicationId).First(application).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					returnUnauthorizedAndRemoveCookie(c)
				}

				err = errors.Wrap(err, fmt.Sprintf("[AuthMiddleware] get application error, application id: %d", tokenData.ApplicationId))
				_ = c.Error(err)
				return
			}

			c.Set("application", application)
		}

		// 将 tokenData 保存到 gin.Context 中
		c.Set("token_data", tokenData)
		c.Set("user", user)
		c.Set("account", account)
		c.Next()
	}
}

func returnUnauthorizedAndRemoveCookie(c *gin.Context) {
	// 如果有 app-token cookie，删除
	if _, err := c.Cookie("app-token"); err == nil {
		c.SetCookie("app-token", "", -1, "/", "", false, true)
	}
	c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
	c.Abort()
}
