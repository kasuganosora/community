package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

// VerifyCredentials handles GET /api/v1/accounts/verify_credentials
func VerifyCredentials(c *gin.Context) {
	account := c.MustGet("account").(*models.Account)

	// Load extra data
	err := db.GetDB().Model(account).Association("Emojis").Find(&account.Emojis)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "load emojis error"))
		return
	}

	account.Extra = &models.AccountExtra{}
	err = db.GetDB().Model(account).Association("Extra").Find(account.Extra)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "load extra error"))
		return
	}

	c.JSON(200, serializer.ToAccount(account))
}

// AccountSource represents the source field in account credentials
type AccountSource struct {
	Privacy   string `json:"privacy"`
	Sensitive bool   `json:"sensitive"`
	Language  string `json:"language"`
	Note      string `json:"note"`
	Fields    []models.AccountField `json:"fields"`
}

// AccountWithSource extends Account with source field
type AccountWithSource struct {
	*serializer.Account
	Source AccountSource `json:"source"`
}
