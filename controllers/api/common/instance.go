package common

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
)

type InstanceResponse struct {
	Domain      string `json:"domain"`
	Title       string `json:"title"`
	Version     string `json:"version"`
	SourceUrl   string `json:"source_url"`
	Description string `json:"description"`
	Usage       struct {
		Users struct {
			ActiveMonth int `json:"active_month"`
		} `json:"users"`
	} `json:"usage"`

	Thumbnail struct {
		Url string `json:"url"`
	} `json:"thumbnail"`

	Languages     []string `json:"languages"`
	Configuration struct {
		Urls struct {
			Streaming string `json:"streaming"`
			Status    string `json:"status"`
		} `json:"urls"`
		Accounts struct {
			MaxFeaturedTags int `json:"max_featured_tags"`
		} `json:"accounts"`

		Statuses struct {
			MaxCharacters            int `json:"max_characters"`
			MaxMediaAttachments      int `json:"max_media_attachments"`
			CharactersReservedPerUrl int `json:"characters_reserved_per_url"`
		} `json:"statuses"`
		MediaAttachments struct {
			SupportedMimeTypes  []string `json:"supported_mime_types"`
			ImageSizeLimit      int      `json:"image_size_limit"`
			ImageMatrixLimit    int      `json:"image_matrix_limit"`
			VideoSizeLimit      int      `json:"video_size_limit"`
			VideoFrameRateLimit int      `json:"video_frame_rate_limit"`
			VideoMatrixLimit    int      `json:"video_matrix_limit"`
		} `json:"media_attachments"`
		Polls struct {
			MaxOptions             int `json:"max_options"`
			MaxCharactersPerOption int `json:"max_characters_per_option"`
			MinExpiration          int `json:"min_expiration"`
			MaxExpiration          int `json:"max_expiration"`
		} `json:"polls"`

		Translation struct {
			Enabled bool `json:"enabled"`
		} `json:"translation"`
	} `json:"configuration"`

	Registrations struct {
		Enabled          bool   `json:"enabled"`
		ApprovalRequired bool   `json:"approval_required"`
		InviteRequired   bool   `json:"invite_required"`
		Message          string `json:"message"`
	} `json:"registrations"`

	Contact struct {
		Email   string `json:"email"`
		Account *serializer.Account
	}

	Rules []ServerRuleResponseItem `json:"rule"`
}

type ServerRuleResponseItem struct {
	Id   string `json:"id"`
	Text string `json:"text"`
}

func GetInstanceDetail(c *gin.Context) {
	resp := InstanceResponse{}
	resp.Domain = systemsettings.GetString(systemsettings.KeyDomain, "")
	resp.Title = systemsettings.GetString(systemsettings.KeyTitle, "")
	resp.Version = config.GetVersion()
	resp.SourceUrl = "https://github.com/kasuganosora/community"
	resp.Description = systemsettings.GetString(systemsettings.KeyDescription, "")
	resp.Usage.Users.ActiveMonth = getActiveUsersCount()
	resp.Thumbnail.Url = systemsettings.GetString(systemsettings.KeyThumbnail, "")
	resp.Languages = systemsettings.GetStrings(systemsettings.KeyLanguages, []string{"zh-CN", "en-US"})
	//resp.Configuration.Urls.Streaming = systemsettings.GetString("config.urls.streaming", nil)
	//resp.Configuration.Urls.Status = systemsettings.GetString("config.urls.status", nil)
	resp.Configuration.Accounts.MaxFeaturedTags = systemsettings.GetInt(systemsettings.KeyAccountMaxFeaturedTags, nil)
	resp.Configuration.Statuses.MaxCharacters = systemsettings.GetInt(systemsettings.KeyStatusMaxCharacters, nil)
	resp.Configuration.Statuses.MaxMediaAttachments = systemsettings.GetInt(systemsettings.KeyStatusMaxMedia, nil)
	resp.Configuration.Statuses.CharactersReservedPerUrl = systemsettings.GetInt(systemsettings.KeyStatusCharactersPerURL, nil)
	resp.Configuration.MediaAttachments.SupportedMimeTypes = systemsettings.GetStrings(systemsettings.KeyMediaMimeTypes, nil)
	resp.Configuration.MediaAttachments.ImageSizeLimit = systemsettings.GetInt(systemsettings.KeyMediaImageSizeLimit, nil)
	resp.Configuration.MediaAttachments.ImageMatrixLimit = systemsettings.GetInt(systemsettings.KeyMediaImageMatrixLimit, nil)
	resp.Configuration.MediaAttachments.VideoSizeLimit = systemsettings.GetInt(systemsettings.KeyMediaVideoSizeLimit, nil)
	resp.Configuration.MediaAttachments.VideoFrameRateLimit = systemsettings.GetInt(systemsettings.KeyMediaVideoFrameRate, nil)
	resp.Configuration.MediaAttachments.VideoMatrixLimit = systemsettings.GetInt(systemsettings.KeyMediaVideoMatrixLimit, nil)
	resp.Configuration.Polls.MaxOptions = systemsettings.GetInt(systemsettings.KeyPollMaxOptions, nil)
	resp.Configuration.Polls.MaxCharactersPerOption = systemsettings.GetInt(systemsettings.KeyPollMaxCharactersPerOption, nil)
	resp.Configuration.Polls.MinExpiration = systemsettings.GetInt(systemsettings.KeyPollMinExpiration, nil)
	resp.Configuration.Polls.MaxExpiration = systemsettings.GetInt(systemsettings.KeyPollMaxExpiration, nil)
	resp.Configuration.Translation.Enabled = systemsettings.GetBool(systemsettings.KeyIsEnableTranslation, nil)
	resp.Registrations.Enabled = systemsettings.GetBool(systemsettings.KeyIsEnableRegistration, true)
	resp.Registrations.ApprovalRequired = systemsettings.GetBool(systemsettings.KeyRegistrationApprovalRequired, false)
	resp.Registrations.InviteRequired = systemsettings.GetBool(systemsettings.KeyRegistrationInviteRequired, false)
	resp.Registrations.Message = systemsettings.GetString(systemsettings.KeyRegistrationMessage, "")
	resp.Contact.Email = systemsettings.GetString(systemsettings.KeySiteContactEmail, "admin@admin.com")

	c.JSON(200, resp)
}

// getActiveUsersCount returns the number of active users in the last 30 days
func getActiveUsersCount() int {
	// Count users who have logged in or posted in the last 30 days
	var count int64
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)

	// Count local accounts with recent activity
	db.GetDB().Model(&models.Account{}).
		Where("domain IS NULL").
		Where("last_activity_at >= ? OR last_status_at >= ? OR created_at >= ?", 
			thirtyDaysAgo, thirtyDaysAgo, thirtyDaysAgo).
		Count(&count)

	return int(count)
}
