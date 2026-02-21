package systemsettings

import (
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"sort"
	"strings"
)

const (
	KeyDomain                       = "domain"                                          // 网站域名
	KeyTitle                        = "title"                                           // 网站标题
	KeyDescription                  = "description"                                     // 网站描述
	KeyThumbnail                    = "thumbnail"                                       // 网站缩略图
	KeyLanguages                    = "languages"                                       // 网站语言
	KeyAccountMaxFeaturedTags       = "config.accounts.max_featured_tags"               // 每个账号最多可设置的标签数量
	KeyStatusMaxCharacters          = "config.statuses.max_characters"                  // 每条推文最多可输入的字符数 0为不限制
	KeyStatusMaxMedia               = "config.statuses.max_media_attachments"           // 每条推文最多可上传的附件数量 0为不限制
	KeyStatusCharactersPerURL       = "config.statuses.characters_reserved_per_url"     // 每条推文中每个URL占用的字符数
	KeyMediaMimeTypes               = "config.media_attachments.supported_mime_types"   // 支持的附件类型
	KeyMediaImageSizeLimit          = "config.media_attachments.image_size_limit"       // 图片附件大小限制
	KeyMediaImageMatrixLimit        = "config.media_attachments.image_matrix_limit"     // 图片附件矩阵限制
	KeyMediaVideoSizeLimit          = "config.media_attachments.video_size_limit"       // 视频附件大小限制
	KeyMediaVideoFrameRate          = "config.media_attachments.video_frame_rate_limit" // 视频附件帧率限制
	KeyMediaVideoMatrixLimit        = "config.media_attachments.video_matrix_limit"     // 视频附件矩阵限制
	KeyPollMaxOptions               = "config.polls.max_options"                        // 投票最多选项
	KeyPollMaxCharactersPerOption   = "config.polls.max_characters_per_option"          // 每个投票选项最多可输入的字符数
	KeyPollMinExpiration            = "config.polls.min_expiration"                     // 投票最短有效期
	KeyPollMaxExpiration            = "config.polls.max_expiration"                     // 投票最长有效期
	KeyIsEnableTranslation          = "config.translations.enabled"                     // 是否启用翻译
	KeyIsEnableRegistration         = "config.registration.enabled"                     // 是否启用注册
	KeyRegistrationApprovalRequired = "config.registration.approval_required"           // 是否需要管理员审核
	KeyRegistrationInviteRequired   = "config.registration.invite_required"             // 是否需要邀请码
	KeyRegistrationMessage          = "config.registration.message"                     // 注册页面的提示信息
	KeySiteContactEmail             = "contact.email"                                   // 网站联系邮箱
	KeyRecaptchaSiteKey             = "config.recaptcha.site_key"                       // recaptcha site key (deprecated, use config.captcha.site_key)
	KeyRecaptchaSecretKey           = "config.recaptcha.secret_key"                     // recaptcha secret key (deprecated, use config.captcha.secret_key)
	KeyRecaptchaEnabled             = "config.recaptcha.enabled"                        // 是否启用recaptcha (deprecated, use config.captcha.enabled)
	
	// 新的通用验证码配置
	KeyCaptchaProvider            = "config.captcha.provider"            // 验证码供应商: recaptcha, cloudflare, hcaptcha, none
	KeyCaptchaSiteKey             = "config.captcha.site_key"            // 验证码站点密钥
	KeyCaptchaSecretKey           = "config.captcha.secret_key"          // 验证码密钥
	KeyCaptchaEnabled             = "config.captcha.enabled"             // 是否启用验证码
	KeyCaptchaScoreThreshold      = "config.captcha.score_threshold"     // 验证码分数阈值 (0.0 - 1.0)
	KeyRegistrationCheckEmail       = "config.registration.check_email"                 // 是否检查邮箱
	KeyMediaAttachmentsBaseURL      = "config.media_attachments.base_url"               // 附件基础URL
	KeyMediaAttachmentsCacheBaseURL = "config.media_attachments.cache_base_url"         // 附件缓存基础URL
	KeyDefaultPageSize              = "config.default_page_limit"                       // 默认分页大小

)

var settings = map[string]any{}
var loaded = false

func Load() {
	if loaded {
		return
	}

	rows := make([]models.SystemSetting, 0)
	err := db.GetDB().Model(&models.SystemSetting{}).Find(&rows).Error
	log.FatalIf(err)

	for _, row := range rows {
		settings[row.Key] = row.Val
	}

	loaded = true
}

func Get(key string, defaultValue any) any {
	Load()

	// 优先取.env中的配置
	if v, ok := config.C.Other[key]; ok {
		return v
	}

	// 没有则在数据库中取
	if v, ok := settings[key]; ok {
		return v
	}

	return defaultValue
}

func GetInt(key string, defaultValue any) int {
	v := Get(key, defaultValue)
	if v == nil {
		return 0
	}

	return utils.ToInt(v)
}

func GetInt64(key string, defaultValue any) int64 {
	v := Get(key, defaultValue)
	if v == nil {
		return 0
	}

	return utils.ToInt64(v)
}

func GetFloat64(key string, defaultValue any) float64 {
	v := Get(key, defaultValue)
	if v == nil {
		return 0
	}

	return utils.ToFloat64(v)
}

func GetBool(key string, defaultValue any) bool {
	v := Get(key, defaultValue)
	if v == nil {
		return false
	}

	return utils.ToBool(v)
}

func GetString(key string, defaultValue string) string {
	v := Get(key, defaultValue)
	if v == nil {
		return ""
	}

	return utils.ToString(v)
}

func GetStrings(key string, defaultValue any) []string {
	v := Get(key, defaultValue)
	if v == nil {
		return nil
	}

	ret := make([]string, 0)
	for _, item := range v.([]any) {
		ret = append(ret, item.(string))
	}

	return ret
}

func GetMap(key string, defaultValue any) map[string]any {
	v := Get(key, defaultValue)
	if v == nil {
		return nil
	}

	return v.(map[string]any)
}

func GetInts(key string, defaultValue any) []int {
	v := Get(key, defaultValue)
	if v == nil {
		return nil
	}

	ret := make([]int, 0)
	for _, item := range v.([]any) {
		ret = append(ret, item.(int))
	}

	return ret
}

func GetInt64s(key string, defaultValue any) []int64 {
	v := Get(key, defaultValue)
	if v == nil {
		return nil
	}

	ret := make([]int64, 0)
	for _, item := range v.([]any) {
		ret = append(ret, item.(int64))
	}

	return ret
}

func Set(key string, val any) {
	settings[key] = val
	return
}

func SaveToDB(keys ...string) (err error) {
	rows := make([]models.SystemSetting, 0)
	err = db.GetDB().Model(&models.SystemSetting{}).Where("key in (?)", keys).Find(&rows).Error

	if err != nil {
		err = errors.Wrapf(err, "failed to get system settings from db, keys: %v", keys)
		return
	}

	existsKeyMap := make(map[string]bool)
	for _, row := range rows {
		existsKeyMap[row.Key] = true
	}

	err = db.GetDB().Transaction(func(tx *gorm.DB) (err error) {
		for _, key := range keys {
			if _, ok := existsKeyMap[key]; ok {
				err = tx.Model(&models.SystemSetting{}).Where("key = ?", key).Update("val", settings[key]).Error
			} else {
				err = tx.Model(&models.SystemSetting{}).Create(&models.SystemSetting{
					Key: key,
					Val: settings[key],
				}).Error
			}

			if err != nil {
				return
			}
		}

		return
	})

	if err != nil {
		err = errors.Wrapf(err, "failed to save system settings to db, keys: %v", keys)
		return
	}

	return
}

// GetAllFlatStyle 获取所有扁平化样式
// 例如： key a.b.c, val 1
// 返回： map[a.b.c]1
func GetAllFlatStyle() (settings map[string]any) {
	return settings
}

// GetAllTreeStyle 获取所有树形样式
// 例如： key a.b.c, val 1
// 返回： map[a][b][c]1
func GetAllTreeStyle() (settings map[string]any) {
	ret := make(map[string]any)
	keys := make([]string, 0)

	for key, _ := range settings {
		keys = append(keys, key)
	}

	// 把key 升序排序
	sort.Sort(sort.StringSlice(keys))

	for _, key := range keys {
		val := settings[key]
		keyParts := strings.Split(key, ".")
		keyLen := len(keyParts)

		for i := 0; i < keyLen; i++ {
			if i == keyLen-1 {
				ret[keyParts[i]] = val
				break
			}

			if _, ok := ret[keyParts[i]]; !ok {
				ret[keyParts[i]] = make(map[string]any)
			}

			ret = ret[keyParts[i]].(map[string]any)
		}

	}

	return
}

func GetDefaultPageSize() int {
	return GetInt(KeyDefaultPageSize, 20)
}

func GetDomain() string {
	return GetString(KeyDomain, "")
}
