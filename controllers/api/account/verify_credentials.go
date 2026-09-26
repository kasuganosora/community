package account

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

// VerifyCredentials handles GET /api/v1/accounts/verify_credentials.
func VerifyCredentials(c *gin.Context) {
	account := c.MustGet("account").(*models.Account) //nolint:forcetypeassert

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

	// note is rendered HTML; source.note is the raw text the user typed and
	// is what clients use to prefill the profile editor (Mastodon API).
	c.JSON(200, AccountWithSource{
		Account: serializer.ToAccount(account),
		Source: AccountSource{
			Privacy:   "public",
			Sensitive: false,
			Note:      account.Note,
			Fields:    []models.AccountField{},
		},
	})
}

// AccountSource represents the source field in account credentials.
type AccountSource struct {
	Privacy   string                `json:"privacy"`
	Sensitive bool                  `json:"sensitive"`
	Language  string                `json:"language"`
	Note      string                `json:"note"`
	Fields    []models.AccountField `json:"fields"`
}

// AccountWithSource extends Account with source field.
type AccountWithSource struct {
	*serializer.Account

	Source AccountSource `json:"source"`
}
