package activitypub

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

const publicAddress = "https://www.w3.org/ns/activitystreams#Public"

func accountActorURI(account *models.Account, baseURL string) string {
	return account.ActorURI(baseURL)
}
func statusObjectURI(status *models.Status, account *models.Account, baseURL string) string {
	if status.OriginUri != nil && *status.OriginUri != "" {
		return *status.OriginUri
	}
	if status.LocalObjectURI != "" {
		return status.LocalObjectURI
	}
	return account.ActorURI(baseURL) + "/statuses/" + strconv.FormatUint(status.Id, 10)
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
	if status == nil || status.LocalOnly {
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
	uri := systemsettings.GetBaseURL() + c.Request.URL.Path
	var status models.Status
	query := db.GetDB().Preload("Account").Preload("Content").Preload("Attachments").Preload("ToAccounts").Preload("Tags").Preload("Emojis").Preload("ReplyAccount")
	err := query.Session(&gorm.Session{}).Where("(local_object_uri = ? OR (origin_url = ? AND (origin_uri IS NULL OR origin_uri = ''))) AND deleted_at IS NULL AND scheduled_at IS NULL AND (reblog_id IS NULL OR reblog_id = 0)", uri, uri).First(&status).Error
	var account models.Account
	if err == nil {
		if status.Account == nil || status.Account.Domain != nil || !status.IsLocal() {
			c.Status(http.StatusNotFound)
			return
		}
		account = *status.Account
	} else {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.Status(http.StatusInternalServerError)
			return
		}
		id, parseErr := strconv.ParseUint(c.Param("id"), 10, 64)
		if parseErr != nil || id == 0 || c.Param("username") == "" {
			c.Status(http.StatusNotFound)
			return
		}
		if err = localActorRouteQuery(db.GetDB(), c).First(&account).Error; err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		if err = query.Where("id = ? AND account_id = ? AND deleted_at IS NULL AND scheduled_at IS NULL AND (reblog_id IS NULL OR reblog_id = 0) AND (local_object_uri = '' OR local_object_uri IS NULL)", id, account.Id).First(&status).Error; err != nil {
			// A boost's status URI answers with its Announce.
			if !localReblogAnnounce(c, &account, id) {
				c.Status(http.StatusNotFound)
			}
			return
		}
	}
	if status.LocalOnly {
		c.Status(http.StatusNotFound)
		return
	}
	if !signedFetchAllowed(c, &account) {
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

// ImportedStatusObject resolves an exact preserved local object URI (Misskey
// /notes/:id and Mastodon numeric object aliases) with normal Note authorization.
func ImportedStatusObject(c *gin.Context) {
	if strings.Contains(c.GetHeader("Accept"), "text/html") && !strings.Contains(c.GetHeader("Accept"), "application/activity+json") && !strings.Contains(c.GetHeader("Accept"), "application/ld+json") {
		var status models.Status
		uri := strings.TrimRight(systemsettings.GetBaseURL(), "/") + c.Request.URL.Path
		if db.GetDB().Preload("Account").Where("local_object_uri = ? AND deleted_at IS NULL AND scheduled_at IS NULL", uri).First(&status).Error != nil || status.Account == nil || status.Account.Domain != nil || !status.IsLocal() || (status.Visibility != models.StatusVisibilityPublic && status.Visibility != models.StatusVisibilityUnlisted) {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Vary", "Accept")
		c.Redirect(http.StatusFound, "/@"+status.Account.Username+"/status/"+strconv.FormatUint(status.Id, 10))
		return
	}
	c.Header("Vary", "Accept")
	if importedReblogAnnounce(c, strings.TrimRight(systemsettings.GetBaseURL(), "/")+c.Request.URL.Path) {
		return
	}
	StatusObject(c)
}

// ImportedStatusActivity serves Misskey's /notes/:id/activity for imported
// boosts (the id Misskey used for their Announce). Only preserved object
// URIs match, so c.hime.at's own boost URLs are unaffected.
func ImportedStatusActivity(c *gin.Context) {
	c.Header("Vary", "Accept")
	uri := strings.TrimRight(systemsettings.GetBaseURL(), "/") + strings.TrimSuffix(c.Request.URL.Path, "/activity")
	if !importedReblogAnnounce(c, uri) {
		c.Status(http.StatusNotFound)
	}
}

// importedReblogAnnounce answers with the Announce of an imported local boost
// whose preserved object URI (Misskey /notes/<id>) is uri. Like Misskey the
// Announce id is <uri>/activity. It reports whether it handled the request.
func importedReblogAnnounce(c *gin.Context, uri string) bool {
	var boost models.Status
	err := db.GetDB().Preload("Account").Where("local_object_uri = ? AND reblog_id IS NOT NULL AND reblog_id > 0 AND deleted_at IS NULL AND scheduled_at IS NULL", uri).First(&boost).Error
	if err != nil {
		return false
	}
	serveReblogAnnounce(c, &boost)
	return true
}

// localReblogAnnounce answers with the Announce of a native local boost
// (status URI <actor>/statuses/<id>, Announce id <that>/activity, as
// delivered by announcePayload). It reports whether id is such a boost.
func localReblogAnnounce(c *gin.Context, account *models.Account, id uint64) bool {
	var boost models.Status
	err := db.GetDB().Where("id = ? AND account_id = ? AND reblog_id IS NOT NULL AND reblog_id > 0 AND deleted_at IS NULL AND scheduled_at IS NULL AND (local_object_uri = '' OR local_object_uri IS NULL)", id, account.Id).First(&boost).Error
	if err != nil {
		return false
	}
	boost.Account = account
	serveReblogAnnounce(c, &boost)
	return true
}

// LocalStatusActivity serves <actor>/statuses/<id>/activity: the Announce of
// a local boost (the id its delivery used). Other statuses are not served here.
func LocalStatusActivity(c *gin.Context) {
	c.Header("Vary", "Accept")
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	var account models.Account
	if err != nil || id == 0 || localActorRouteQuery(db.GetDB(), c).First(&account).Error != nil || !localReblogAnnounce(c, &account, id) {
		c.Status(http.StatusNotFound)
	}
}

// serveReblogAnnounce writes a boost's Announce: public/unlisted boosts by a
// local account of a still visible (public/unlisted) original only.
func serveReblogAnnounce(c *gin.Context, boost *models.Status) {
	if boost.Account == nil || boost.Account.Domain != nil || boost.LocalOnly ||
		(boost.Visibility != models.StatusVisibilityPublic && boost.Visibility != models.StatusVisibilityUnlisted) {
		c.Status(http.StatusNotFound)
		return
	}
	var original models.Status
	if err := db.GetDB().Preload("Account").Where("id = ? AND deleted_at IS NULL", *boost.ReblogId).First(&original).Error; err != nil || original.Account == nil || original.LocalOnly ||
		(original.Visibility != models.StatusVisibilityPublic && original.Visibility != models.StatusVisibilityUnlisted) {
		c.Status(http.StatusNotFound)
		return
	}
	if !signedFetchAllowed(c, boost.Account) {
		return
	}
	boost.Reblog = &original
	announce, _ := announcePayload(boost, boost.Account, accountActorURI(boost.Account, systemsettings.GetBaseURL()), "", false)
	announce["@context"] = "https://www.w3.org/ns/activitystreams"
	c.Header("Content-Type", "application/activity+json")
	c.JSON(http.StatusOK, announce)
}
