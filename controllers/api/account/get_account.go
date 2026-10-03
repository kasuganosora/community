package account

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func GetAccount(c *gin.Context) {
	accountId := c.Param("id")

	var (
		account *models.Account
		err     error
	)

	if accountId == "me" {
		_, exists := c.Get("account")
		if !exists {
			c.JSON(401, gin.H{"error": "Unauthorized"}) //nolint:goconst

			return
		}

		account = c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	}

	// 尝试从缓存读取（仅对非 "me" 的数字 ID）
	if account == nil {
		var cached models.Account
		cacheKey := fmt.Sprintf(cache.KeyAccountByAcct, accountId)
		if err := cache.GetCache().Get(c.Request.Context(), cacheKey, &cached); err == nil {
			// Extra and Emojis use json:"-" and are not present in cached
			// Account JSON. Always hydrate these associations below; returning
			// the cache directly silently reset counters and fields to zero.
			account = &cached
		} else {
			err = db.GetDB().Where("id = ?", accountId).First(&account).Error
		}
	}

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, gin.H{"error": "Not Found"})

			return
		}

		err = errors.Wrap(err, "GetAccount")
		_ = c.Error(err)

		return
	}

	err = db.GetDB().Model(account).Association("Emojis").Find(&account.Emojis)
	if err != nil {
		err = errors.Wrap(err, "GetAccount")
		_ = c.Error(err)

		return
	}

	account.Extra = &models.AccountExtra{}

	err = db.GetDB().Model(account).Association("Extra").Find(account.Extra)
	if err != nil {
		err = errors.Wrap(err, "GetAccount")
		_ = c.Error(err)

		return
	}

	// 写入缓存（TTL 5 分钟）
	cacheKey := fmt.Sprintf(cache.KeyAccountByAcct, accountId)
	_ = cache.GetCache().Set(c.Request.Context(), cacheKey, account, cache.DefaultExpiration)

	acc := serializer.ToAccount(account)

	c.JSON(200, acc)
}
