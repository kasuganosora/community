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

	suspended, err := models.AccountSuspended(conn, account)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "profile account suspension"))
		return
	}
	origin := models.AccountOriginHost(account)
	originBlocked, originDisclosable, err := models.NodeSuspension(conn, origin)
	if err != nil {
		_ = c.Error(errors.Wrap(err, "profile origin suspension"))
		return
	}
	acc := serializer.ToAccount(account)
	switch {
	case suspended:
		acc.Suspended = true
		acc.Availability = &serializer.AccountAvailability{Reason: models.ProfileAvailabilityAccountSuspended}
	case originBlocked && originDisclosable && !strings.EqualFold(origin, domain):
		acc.Availability = &serializer.AccountAvailability{Reason: models.ProfileAvailabilityDomainSuspended}
	}
	c.JSON(200, acc)
}

func writeWithheldProfile(c *gin.Context, reason string) {
	body := gin.H{"error": "Record not found"}
	if reason != "" {
		body["availability"] = gin.H{"reason": reason}
	}
	c.AbortWithStatusJSON(404, body)
}
