package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type InitSystemConfigOptions struct {
}

func (InitSystemConfigOptions) Up(db *gorm.DB) (err error) {
	// 创建系统配置项内容
	records := make(map[string]any)
	records["domain"] = "example.com"
	records["title"] = "Community"
	records["description"] = "Community"
	records["languages"] = []string{"zh-CN", "en-US"}
	records["config.accounts.max_featured_tags"] = 10
	records["config.statuses.max_characters"] = 500
	records["config.statuses.max_media_attachments"] = 4
	records["config.statuses.characters_reserved_per_url"] = 23
	records["config.media_attachments.supported_mime_types"] = []string{
		"image/jpeg",
		"image/png",
		"image/gif",
		"image/heic",
		"image/heif",
		"image/webp",
		"image/avif",
		"video/webm",
		"video/mp4",
		"video/quicktime",
		"video/ogg",
		"audio/wave",
		"audio/wav",
		"audio/x-wav",
		"audio/x-pn-wave",
		"audio/vnd.wave",
		"audio/ogg",
		"audio/vorbis",
		"audio/mpeg",
		"audio/mp3",
		"audio/webm",
		"audio/flac",
		"audio/aac",
		"audio/m4a",
		"audio/x-m4a",
		"audio/mp4",
		"audio/3gpp",
		"video/x-ms-asf",
	}

	records["config.media_attachments.image_size_limit"] = 41943040
	records["config.media_attachments.image_matrix_limit"] = 16777216
	records["config.media_attachments.video_size_limit"] = 536870912
	records["config.media_attachments.video_frame_rate_limit"] = 120
	records["config.media_attachments.video_matrix_limit"] = 8294400
	records["config.polls.max_options"] = 4
	records["config.polls.max_characters_per_option"] = 50
	records["config.polls.min_expiration"] = 300
	records["config.polls.max_expiration"] = 2629746
	records["config.translation.enabled"] = false
	records["config.registration.enabled"] = true
	records["config.registration.approval_required"] = false
	records["registrations.message"] = ""
	records["contact.email"] = ""

	// 创建系统配置项
	err = db.Transaction(func(tx *gorm.DB) (err error) {
		for key, val := range records {
			err = tx.Model(&models.SystemSetting{}).Where("`key` = ?", key).FirstOrCreate(&models.SystemSetting{Key: key, Val: val}).Error
			if err != nil {
				return
			}
		}
		return
	})

	return
}

func (InitSystemConfigOptions) Down(db *gorm.DB) (err error) {
	// 删除所有系统配置项
	err = db.Delete(&models.SystemSetting{}).Error
	return
}
