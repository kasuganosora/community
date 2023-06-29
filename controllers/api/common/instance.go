package common

import (
	"github.com/gin-gonic/gin"
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
		Message          string `json:"message"`
	} `json:"registrations"`

	Contact struct {
		Email string `json:"email"`
		// TODO Account
	}

	Rules []ServerRuleResponseItem `json:"rules"`
}

type ServerRuleResponseItem struct {
	Id   string `json:"id"`
	Text string `json:"text"`
}

func GetInstanceDetail(c *gin.Context) {
	resp := InstanceResponse{}
	resp.Domain = systemsettings.GetString("domain", nil)
	resp.Title = systemsettings.GetString("title", nil)
	resp.Version = systemsettings.GetString("version", 1)
	resp.SourceUrl = "https://github.com/kasuganosora/community"
	resp.Description = systemsettings.GetString("description", nil)
	resp.Usage.Users.ActiveMonth = 0
	resp.Thumbnail.Url = systemsettings.GetString("thumbnail", nil)
	resp.Languages = systemsettings.GetStrings("languages", []string{"zh-CN", "en-US"})
	//resp.Configuration.Urls.Streaming = systemsettings.GetString("config.urls.streaming", nil)
	//resp.Configuration.Urls.Status = systemsettings.GetString("config.urls.status", nil)
	resp.Configuration.Accounts.MaxFeaturedTags = systemsettings.GetInt("config.accounts.max_featured_tags", nil)
	resp.Configuration.Statuses.MaxCharacters = systemsettings.GetInt("config.statuses.max_characters", nil)
	resp.Configuration.Statuses.MaxMediaAttachments = systemsettings.GetInt("config.statuses.max_media_attachments", nil)
	resp.Configuration.Statuses.CharactersReservedPerUrl = systemsettings.GetInt("config.statuses.characters_reserved_per_url", nil)
	resp.Configuration.MediaAttachments.SupportedMimeTypes = systemsettings.GetStrings("config.media_attachments.supported_mime_types", nil)
	resp.Configuration.MediaAttachments.ImageSizeLimit = systemsettings.GetInt("config.media_attachments.image_size_limit", nil)
	resp.Configuration.MediaAttachments.ImageMatrixLimit = systemsettings.GetInt("config.media_attachments.image_matrix_limit", nil)
	resp.Configuration.MediaAttachments.VideoSizeLimit = systemsettings.GetInt("config.media_attachments.video_size_limit", nil)
	resp.Configuration.MediaAttachments.VideoFrameRateLimit = systemsettings.GetInt("config.media_attachments.video_frame_rate_limit", nil)
	resp.Configuration.MediaAttachments.VideoMatrixLimit = systemsettings.GetInt("config.media_attachments.video_matrix_limit", nil)
	resp.Configuration.Polls.MaxOptions = systemsettings.GetInt("config.polls.max_options", nil)
	resp.Configuration.Polls.MaxCharactersPerOption = systemsettings.GetInt("config.polls.max_characters_per_option", nil)
	resp.Configuration.Polls.MinExpiration = systemsettings.GetInt("config.polls.min_expiration", nil)
	resp.Configuration.Polls.MaxExpiration = systemsettings.GetInt("config.polls.max_expiration", nil)
	resp.Configuration.Translation.Enabled = systemsettings.GetBool("config.translation.enabled", nil)
	resp.Registrations.Enabled = systemsettings.GetBool("registrations.enabled", true)
	resp.Registrations.ApprovalRequired = systemsettings.GetBool("registrations.approval_required", false)
	resp.Registrations.Message = systemsettings.GetString("registrations.message", nil)
	resp.Contact.Email = systemsettings.GetString("contact.email", "admin@admin.com")

	c.JSON(200, resp)
}
