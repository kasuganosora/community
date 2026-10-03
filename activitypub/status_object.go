package activitypub

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

const publicAddress = "https://www.w3.org/ns/activitystreams#Public"

func accountActorURI(account *models.Account, baseURL string) string {
	if account.Domain != nil && account.OriginId != "" {
		return account.OriginId
	}
	return baseURL + "/users/" + account.Username
}
func statusObjectURI(status *models.Status, account *models.Account, baseURL string) string {
	if status.OriginUri != nil && *status.OriginUri != "" {
		return *status.OriginUri
	}
	return accountActorURI(account, baseURL) + "/statuses/" + strconv.FormatUint(status.Id, 10)
}
func mentionName(account *models.Account) string {
	name := "@" + account.Username
	if account.Domain != nil {
		name += "@" + *account.Domain
	}
	return name
}
func statusRecipients(status *models.Status, account *models.Account, baseURL string) ([]string, []string) {
	mentions := make([]string, 0, len(status.ToAccounts))
	seen := map[string]bool{}
	for _, recipient := range status.ToAccounts {
		uri := accountActorURI(recipient, baseURL)
		if !seen[uri] {
			mentions = append(mentions, uri)
			seen[uri] = true
		}
	}
	if (status.Visibility == models.StatusVisibilityPublic || status.Visibility == models.StatusVisibilityUnlisted) && status.ReplyAccount != nil {
		uri := accountActorURI(status.ReplyAccount, baseURL)
		if !seen[uri] {
			mentions = append(mentions, uri)
		}
	}
	followers := accountActorURI(account, baseURL) + "/followers"
	switch status.Visibility {
	case models.StatusVisibilityPublic:
		return []string{publicAddress}, append([]string{followers}, mentions...)
	case models.StatusVisibilityUnlisted:
		return append([]string{followers}, mentions...), []string{publicAddress}
	case models.StatusVisibilityPrivate:
		return append([]string{followers}, mentions...), []string{}
	default:
		return mentions, []string{}
	}
}

// StatusToNote is shared by object retrieval, collections and outbound delivery.
// Callers load content, attachments, mentions and tags with the status.
func StatusToNote(status *models.Status) (map[string]any, error) {
	if status == nil {
		return nil, errors.New("status is required")
	}
	if status.Account == nil {
		var account models.Account
		if err := db.GetDB().First(&account, status.AccountId).Error; err != nil {
			return nil, err
		}
		status.Account = &account
	}
	return buildNoteFromStatus(status, status.Account, systemsettings.GetBaseURL()), nil
}

// StatusObject exposes canonical local Note objects. Private objects require a
// valid federation signature AND permission for its actor; possession of a URL
// (or an unrelated actor key) never grants access.
func StatusObject(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		c.Status(http.StatusNotFound)
		return
	}
	var account models.Account
	if err = db.GetDB().Where("username = ? AND domain IS NULL", c.Param("username")).First(&account).Error; err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	var status models.Status
	query := db.GetDB().Preload("Content").Preload("Attachments").Preload("ToAccounts").Preload("Tags").Preload("ReplyAccount")
	if err = query.Where("id = ? AND account_id = ? AND deleted_at IS NULL AND scheduled_at IS NULL AND reblog_id IS NULL", id, account.Id).First(&status).Error; err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	if status.Visibility != models.StatusVisibilityPublic && status.Visibility != models.StatusVisibilityUnlisted {
		c.Header("Cache-Control", "private, no-store")
		actor, verifyErr := VerifyActivitySignature(c, nil)
		if verifyErr != nil {
			c.Status(http.StatusUnauthorized)
			return
		}
		var viewer models.Account
		if err = db.GetDB().Where("origin_id = ? AND domain IS NOT NULL", actor).First(&viewer).Error; err != nil {
			c.Status(http.StatusForbidden)
			return
		}
		visible, checkErr := status.CanViewEx(db.GetDB(), &viewer)
		if checkErr != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		if !visible {
			c.Status(http.StatusForbidden)
			return
		}

	}
	status.Account = &account
	note, err := StatusToNote(&status)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Header("Content-Type", "application/activity+json")
	c.JSON(http.StatusOK, note)
}
