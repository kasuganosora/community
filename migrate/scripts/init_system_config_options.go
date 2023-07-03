package scripts

import (
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

type InitSystemConfigOptions struct {
}

func (InitSystemConfigOptions) Up(db *gorm.DB) (err error) {
	// 创建系统配置项内容
	records := make(map[string]any)
	records[systemsettings.KeyDomain] = "example.com"
	records[systemsettings.KeyTitle] = "Community"
	records[systemsettings.KeyDescription] = "Community"
	records[systemsettings.KeyLanguages] = []string{"zh-CN", "en-US"}
	records[systemsettings.KeyAccountMaxFeaturedTags] = 10
	records[systemsettings.KeyStatusMaxCharacters] = 500
	records[systemsettings.KeyStatusMaxMedia] = 4
	records[systemsettings.KeyStatusCharactersPerURL] = 23
	records[systemsettings.KeyMediaMimeTypes] = []string{
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

	records[systemsettings.KeyMediaImageSizeLimit] = 41943040
	records[systemsettings.KeyMediaImageMatrixLimit] = 16777216
	records[systemsettings.KeyMediaVideoSizeLimit] = 536870912
	records[systemsettings.KeyMediaVideoFrameRate] = 120
	records[systemsettings.KeyMediaVideoMatrixLimit] = 8294400
	records[systemsettings.KeyPollMaxOptions] = 4
	records[systemsettings.KeyPollMaxCharactersPerOption] = 50
	records[systemsettings.KeyPollMinExpiration] = 300
	records[systemsettings.KeyPollMaxExpiration] = 2629746
	records[systemsettings.KeyIsEnableTranslation] = false
	records[systemsettings.KeyIsEnableRegistration] = true
	records[systemsettings.KeyRegistrationApprovalRequired] = false
	records[systemsettings.KeyRegistrationMessage] = ""
	records[systemsettings.KeySiteContactEmail] = ""
	records[systemsettings.KeyRegistrationInviteRequired] = false
	records[systemsettings.KeyRecaptchaSiteKey] = ""
	records[systemsettings.KeyRecaptchaSecretKey] = ""
	records[systemsettings.KeyRecaptchaEnabled] = false
	records[systemsettings.KeyRegistrationCheckEmail] = false

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
