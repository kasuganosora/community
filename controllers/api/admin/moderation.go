package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/kasuganosora/community/systemsettings"
)

// moderationView is the admin "管理" (Misskey admin/moderation) settings.
func moderationView() gin.H {
	s := moderation.Current()
	view := gin.H{
		"enable_registration":               systemsettings.GetBool(systemsettings.KeyIsEnableRegistration, false),
		"email_required_for_signup":         systemsettings.GetBool(systemsettings.KeyRegistrationCheckEmail, false),
		"auto_close_registration":           s.AutoCloseRegistration,
		"registration_auto_closed_at":       systemsettings.GetString(moderation.KeyRegistrationAutoClosedAt, ""),
		"ugc_visibility_for_visitor":        s.UGCVisibilityForVisitor,
		"preserved_usernames":               nonNil(s.PreservedUsernames),
		"sensitive_words":                   nonNil(s.SensitiveWords),
		"prohibited_words":                  nonNil(s.ProhibitedWords),
		"prohibited_words_for_name_of_user": nonNil(s.ProhibitedWordsForNameOfUser),
		"hidden_tags":                       nonNil(s.HiddenTags),
		"blocked_hosts":                     nonNil(s.BlockedHosts),
		"silenced_hosts":                    nonNil(s.SilencedHosts),
		"media_silenced_hosts":              nonNil(s.MediaSilencedHosts),
		"moderator_inactivity_limit_days":   int(moderation.ModeratorInactivityLimit / (24 * time.Hour)),
	}
	if conn := db.GetDB(); conn != nil {
		if activity, err := moderation.ModeratorActivity(conn); err == nil {
			var newest time.Time
			for _, t := range activity {
				if t.After(newest) {
					newest = t
				}
			}
			if !newest.IsZero() {
				view["moderators_last_active_at"] = newest.UTC().Format(time.RFC3339)
			}
		}
	}
	// Settings pinned by the deployment environment cannot be changed here.
	locked := []string{}
	for key, name := range map[string]string{systemsettings.KeyIsEnableRegistration: "enable_registration", systemsettings.KeyRegistrationCheckEmail: "email_required_for_signup"} {
		if _, ok := systemsettingsEnvironment(key); ok {
			locked = append(locked, name)
		}
	}
	view["environment_locked"] = locked
	return view
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// GetModerationSettings handles GET /api/v1/admin/moderation.
func GetModerationSettings(c *gin.Context) {
	if a := controllers.GetAccount(c); a != nil {
		moderation.TouchModerator(db.GetDB(), a.Id)
	}
	c.JSON(http.StatusOK, moderationView())
}

// UpdateModerationSettings handles PUT /api/v1/admin/moderation. Only the
// fields present in the body change, like Misskey's admin/update-meta.
func UpdateModerationSettings(c *gin.Context) {
	var req struct {
		moderation.Update
		EnableRegistration     *bool `json:"enable_registration"`
		EmailRequiredForSignup *bool `json:"email_required_for_signup"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	changes, err := req.Update.Changes()
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	for key, v := range map[string]*bool{systemsettings.KeyIsEnableRegistration: req.EnableRegistration, systemsettings.KeyRegistrationCheckEmail: req.EmailRequiredForSignup} {
		if v == nil {
			continue
		}
		if _, ok := systemsettingsEnvironment(key); ok {
			c.JSON(http.StatusConflict, gin.H{"error": "setting is managed by deployment"})
			return
		}
		changes[key] = *v
	}
	if req.EnableRegistration != nil && *req.EnableRegistration {
		changes[moderation.KeyRegistrationAutoClosedAt] = ""
	}
	if len(changes) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no changes"})
		return
	}
	if err := moderation.SaveRaw(changes); err != nil {
		_ = c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "settings could not be saved"})
		return
	}
	if a := controllers.GetAccount(c); a != nil {
		moderation.TouchModerator(db.GetDB(), a.Id)
	}
	c.JSON(http.StatusOK, moderationView())
}

// FederationInstanceModeration handles
// POST /api/v1/admin/federation/instances/:domain/moderation with
// {"action": "block"|"unblock"|"suspend"|"unsuspend"|"silence"|"unsilence"|"media_silence"|"media_unsilence"}.
// block/silence/media_silence edit the moderation host lists; suspend is
// Misskey's per-instance delivery suspension.
func FederationInstanceModeration(c *gin.Context) {
	domain := moderation.NormalizeHost(c.Param("domain"))
	if domain == "" || len(domain) > 255 || !queueHostPattern.MatchString(domain) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid domain"})
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "action required"})
		return
	}
	var err error
	switch strings.TrimSpace(req.Action) {
	case "block", "unblock":
		err = moderation.SetHostListed(moderation.KeyBlockedHosts, domain, req.Action == "block")
		if err == nil && req.Action == "unblock" {
			// Also lift a legacy (Mastodon admin) suspending domain block.
			err = moderation.LiftLegacyDomainBlock(db.GetDB().WithContext(c.Request.Context()), domain, "suspend")
		}
	case "silence", "unsilence":
		err = moderation.SetHostListed(moderation.KeySilencedHosts, domain, req.Action == "silence")
		if err == nil && req.Action == "unsilence" {
			err = moderation.LiftLegacyDomainBlock(db.GetDB().WithContext(c.Request.Context()), domain, "silence")
		}
	case "media_silence", "media_unsilence":
		err = moderation.SetHostListed(moderation.KeyMediaSilencedHosts, domain, req.Action == "media_silence")
	case "suspend", "unsuspend":
		err = moderation.SetInstanceSuspended(db.GetDB().WithContext(c.Request.Context()), domain, req.Action == "suspend")
	default:
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "unknown action"})
		return
	}
	if err != nil {
		_ = c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "operation could not be completed"})
		return
	}
	c.JSON(http.StatusOK, instanceModerationState(domain))
}

func instanceModerationState(domain string) gin.H {
	s := moderation.Current()
	suspend, silence, err := models.DomainSeverities(db.GetDB())
	if err != nil {
		suspend, silence = models.HostSet{}, models.HostSet{}
	}
	return gin.H{
		"domain":            domain,
		"is_blocked":        s.IsBlockedHost(domain) || suspend.Contains(domain),
		"is_silenced":       s.IsSilencedHost(domain) || silence.Contains(domain),
		"is_media_silenced": s.IsMediaSilencedHost(domain),
		"is_suspended":      moderation.IsSuspendedInstance(domain),
	}
}

// systemsettingsEnvironment reports a value pinned by the environment
// (config.C.Other overrides system_settings in systemsettings.Get).
func systemsettingsEnvironment(key string) (any, bool) {
	v, ok := config.C.Other[key]
	return v, ok
}
