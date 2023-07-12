package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
	"gorm.io/gorm"
)

func GetAccount(c *gin.Context) {
	accountId := c.Param("id")

	var account *models.Account
	var err error
	if accountId == "me" {
		if !c.GetBool("authenticated") {
			c.JSON(401, gin.H{"error": "Unauthorized"})
			return
		}

		account = c.MustGet("account").(*models.Account)
	}

	if account == nil {
		err = db.GetDB().Where("id = ?", accountId).First(&account).Error
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

	err = db.GetDB().Association("Emojis").Find(&account.Emojis)
	if err != nil {
		err = errors.Wrap(err, "GetAccount")
		_ = c.Error(err)
		return
	}

	err = db.GetDB().Association("Extra").Find(&account.Emojis)
	if err != nil {
		err = errors.Wrap(err, "GetAccount")
		_ = c.Error(err)
		return
	}

	acc := serializer.ToAccount(account)

	c.JSON(200, acc)

}
