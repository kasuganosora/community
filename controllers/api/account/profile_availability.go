package account

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/pkg/errors"
)

// finishProfileLookup writes the lookup response without changing the status
// codes lookup already used: a withheld node stays 404, an account document
// stays 200. The availability object is the only addition, and only when the
// public-safe reason from models.NodeSuspension / AccountSuspended allows it.
func finishProfileLookup(c *gin.Context, account *models.Account, queryDomain string) {
	conn := db.GetDB()
	domain := queryDomain
	if account != nil && account.Domain != nil && strings.TrimSpace(*account.Domain) != "" {
		domain = *account.Domain
	}
	blocked, disclosable, err := models.NodeSuspension(conn, domain)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "profile node suspension"))
		return
	}
	if blocked {
		reason := ""
		if disclosable {
			reason = models.ProfileAvailabilityDomainSuspended
		}
		writeWithheldProfile(c, reason)
		return
	}
	if account == nil {
		writeWithheldProfile(c, "")
		return
	}

	var viewer *models.Account
	if c.GetBool("authenticated") {
		viewer = c.MustGet("account").(*models.Account) //nolint:forcetypeassert
	}
	kept, err := models.FilterAccountsByDomainPolicy(conn, viewer, []*models.Account{account})
	if err != nil {
		_ = c.Error(errors.Wrap(err, "filter blocked domains"))
		return
	}
	if len(kept) == 0 {
		// The viewer's own domain block. That is not an instance ban.
		writeWithheldProfile(c, "")
		return
	}

	acc := serializer.ToAccount(account)
	if err := applyPublicAvailability(account, acc); err != nil {
		_ = c.Error(errors.Wrap(err, "profile availability"))
		return
	}
	c.JSON(200, acc)
}

// applyPublicAvailability sets the same public reason lookup uses, without
// changing HTTP status. A disclosable acct-domain node block outranks an
// account suspension. An obfuscated block adds nothing. Private comments are
// never copied. The suspension flags are read from the current row so a
// cached account document cannot keep a stale ban or hide a new one.
func applyPublicAvailability(account *models.Account, acc *serializer.Account) error {
	if account == nil || acc == nil {
		return nil
	}
	refreshSuspensionSnapshot(account)
	conn := db.GetDB()
	domain := ""
	if account.Domain != nil {
		domain = strings.TrimSpace(*account.Domain)
	}
	blocked, disclosable, err := models.NodeSuspension(conn, domain)
	if err != nil {
		return err
	}
	if blocked {
		if disclosable {
			acc.Suspended = false
			acc.Availability = &serializer.AccountAvailability{Reason: models.ProfileAvailabilityDomainSuspended}
		}
		return nil
	}
	suspended, err := models.AccountSuspended(conn, account)
	if err != nil {
		return err
	}
	origin := models.AccountOriginHost(account)
	originBlocked, originDisclosable, err := models.NodeSuspension(conn, origin)
	if err != nil {
		return err
	}
	switch {
	case suspended:
		acc.Suspended = true
		acc.Availability = &serializer.AccountAvailability{Reason: models.ProfileAvailabilityAccountSuspended}
	case originBlocked && originDisclosable && !strings.EqualFold(origin, domain):
		acc.Availability = &serializer.AccountAvailability{Reason: models.ProfileAvailabilityDomainSuspended}
	}
	return nil
}

func refreshSuspensionSnapshot(account *models.Account) {
	conn := db.GetDB()
	if account == nil || account.Id == 0 || conn == nil {
		return
	}
	var current models.Account
	if conn.Select("is_ban", "user_id", "domain", "origin_id").First(&current, account.Id).Error != nil {
		return
	}
	account.IsBan = current.IsBan
	account.UserId = current.UserId
	account.User = nil
	account.Domain = current.Domain
	account.OriginId = current.OriginId
}

func writeWithheldProfile(c *gin.Context, reason string) {
	body := gin.H{"error": "Record not found"}
	if reason != "" {
		body["availability"] = gin.H{"reason": reason}
	}
	c.AbortWithStatusJSON(404, body)
}
