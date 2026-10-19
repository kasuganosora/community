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
	user, _ := c.Get("user")
	currentUser, _ := user.(*models.User)
	permissions, err := models.EffectiveAdminPermissions(db.GetDB().WithContext(c.Request.Context()), currentUser)
	if err != nil {
		c.JSON(500, gin.H{"error": "permission lookup failed"})
		return
	}
	result := serializer.ToAccount(account)
	result.FollowedMessage = account.FollowedMessage
	c.JSON(200, AccountWithSource{
		Account:          result,
		IsAdmin:          currentUser != nil && currentUser.IsAdmin,
		AdminPermissions: models.AdminCapabilities(permissions),
		Source: AccountSource{
			Privacy:             "public",
			Sensitive:           false,
			Note:                account.Note,
			Fields:              sourceFields(account),
			Language:            account.Language,
			FollowRequestsCount: 0,
			HideCollections:     account.HideCollections,
			Discoverable:        account.Discoverable,
			Indexable:           !account.NoIndex,
			FollowingVisibility: account.FollowingVisibilityValue(),
			FollowersVisibility: account.FollowersVisibilityValue(),
			AutoAcceptFollowed:  account.AutoAcceptFollowed,
			HideOnlineStatus:    account.HideOnlineStatus,
			SpeakAsCat:          account.SpeakAsCat,
		},
		Role: roleFor(currentUser),
	})
}

// AccountSource represents the source field in account credentials.
type AccountSource struct {
	Privacy   string             `json:"privacy"`
	Sensitive bool               `json:"sensitive"`
	Language  string             `json:"language"`
	Note      string             `json:"note"`
	Fields    []serializer.Field `json:"fields"`

	FollowRequestsCount int  `json:"follow_requests_count"`
	HideCollections     bool `json:"hide_collections"`
	Discoverable        bool `json:"discoverable"`
	Indexable           bool `json:"indexable"`
	// Misskey account settings (extensions).
	FollowingVisibility string `json:"following_visibility"`
	FollowersVisibility string `json:"followers_visibility"`
	AutoAcceptFollowed  bool   `json:"auto_accept_followed"`
	HideOnlineStatus    bool   `json:"hide_online_status"`
	SpeakAsCat          bool   `json:"speak_as_cat"`
}

// AccountRole mirrors Mastodon's Role entity (CredentialAccount#role).
type AccountRole struct {
	Id          string `json:"id"`
	Name        string `json:"name"`
	Permissions string `json:"permissions"`
	Color       string `json:"color"`
	Highlighted bool   `json:"highlighted"`
}

func roleFor(u *models.User) AccountRole {
	if u != nil && u.IsAdmin {
		return AccountRole{Id: "3", Name: "Admin", Permissions: "1", Color: "", Highlighted: true}
	}

	// Mastodon's implicit "everyone" role.
	return AccountRole{Id: "-99", Name: "", Permissions: "0", Color: "", Highlighted: false}
}

func sourceFields(a *models.Account) []serializer.Field {
	if a.Extra != nil && a.Extra.Fields != nil {
		return serializer.ToFields(a.Extra.Fields)
	}

	return []serializer.Field{}
}

// AccountWithSource extends Account with source field.
type AccountWithSource struct {
	*serializer.Account

	Source           AccountSource `json:"source"`
	Role             AccountRole   `json:"role"`
	IsAdmin          bool          `json:"is_admin"`
	AdminPermissions []string      `json:"admin_permissions"`
}
