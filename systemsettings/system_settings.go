package systemsettings

import (
	"fmt"
	"net/url"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"maps"
	"sort"
	"strings"
	"sync"
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
	KeyRecaptchaSecretKey           = "config.recaptcha.secret_key"                     //nolint:gosec // G101: config key name, not a credential
	KeyRecaptchaEnabled             = "config.recaptcha.enabled"                        // 是否启用recaptcha (deprecated, use config.captcha.enabled)

	// 新的通用验证码配置.
	KeyCaptchaProvider              = "config.captcha.provider"                 // 验证码供应商: recaptcha, cloudflare, hcaptcha, none
	KeyCaptchaSiteKey               = "config.captcha.site_key"                 // 验证码站点密钥
	KeyCaptchaSecretKey             = "config.captcha.secret_key"               //nolint:gosec // G101: config key name, not a credential
	KeyCaptchaEnabled               = "config.captcha.enabled"                  // 是否启用验证码
	KeyCaptchaScoreThreshold        = "config.captcha.score_threshold"          // 验证码分数阈值 (0.0 - 1.0)
	KeyRegistrationCheckEmail       = "config.registration.check_email"         // 是否检查邮箱
	KeyMediaAttachmentsBaseURL      = "config.media_attachments.base_url"       // 附件基础URL
	KeyMediaAttachmentsCacheBaseURL = "config.media_attachments.cache_base_url" // 附件缓存基础URL
	KeyDefaultPageSize              = "config.default_page_limit"               // 默认分页大小

)

var (
	settings = map[string]any{}
	loaded   = false
	mu       sync.RWMutex
)

// Load 从数据库加载系统设置到内存缓存。
// 使用双重检查锁定模式：已加载时仅需要读锁（快速路径），
// 避免在频繁调用的 Get() 中序列化所有读操作。
func Load() {
	// 快速路径：读锁检查是否已加载
	mu.RLock()
	if loaded {
		mu.RUnlock()
		return
	}
	mu.RUnlock()

	mu.Lock()
	defer mu.Unlock()

	// 获取写锁后再次检查，防止并发重复加载
	if loaded {
		return
	}

	dbInstance := db.GetDB()
	if dbInstance == nil {
		loaded = true
		return
	}

	rows := make([]models.SystemSetting, 0)

	err := dbInstance.Model(&models.SystemSetting{}).Find(&rows).Error
	if err != nil {
		// Table may not exist in standalone/complement mode — skip gracefully
		log.Error("systemsettings.Load: %v (continuing without system settings)", err)

		loaded = true

		return
	}

	for _, row := range rows {
		settings[row.Key] = row.Val
	}

	loaded = true
}

func Get(key string, defaultValue any) any {
	Load()

	mu.RLock()
	defer mu.RUnlock()

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

	// Handle various slice types safely
	ret := make([]string, 0)
	switch s := v.(type) {
	case []string:
		ret = append(ret, s...)
	case []any:
		for _, item := range s {
			str, ok := item.(string)
			if !ok {
				continue
			}
			ret = append(ret, str)
		}
	case string:
		if s != "" {
			ret = append(ret, s)
		}
	}

	return ret
}

func GetMap(key string, defaultValue any) map[string]any {
	v := Get(key, defaultValue)
	if v == nil {
		return nil
	}

	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func GetInts(key string, defaultValue any) []int {
	v := Get(key, defaultValue)
	if v == nil {
		return nil
	}

	ret := make([]int, 0)
	switch s := v.(type) {
	case []int:
		ret = append(ret, s...)
	case []any:
		for _, item := range s {
			if i, ok := item.(int); ok {
				ret = append(ret, i)
			}
		}
	case int:
		ret = append(ret, s)
	}

	return ret
}

func GetInt64s(key string, defaultValue any) []int64 {
	v := Get(key, defaultValue)
	if v == nil {
		return nil
	}

	ret := make([]int64, 0)
	switch s := v.(type) {
	case []int64:
		ret = append(ret, s...)
	case []any:
		for _, item := range s {
			if i, ok := item.(int64); ok {
				ret = append(ret, i)
			}
		}
	case int64:
		ret = append(ret, s)
	}

	return ret
}

func Set(key string, val any) {
	mu.Lock()
	defer mu.Unlock()
	settings[key] = val
}

// SaveToDB 将指定 keys 对应的内存缓存值持久化到数据库。
//
// 修复的问题：
//   - db.GetDB() 可能为 nil（standalone/test 模式），旧代码直接调用会 nil pointer panic。
//   - 旧代码在事务回调中直接读取全局 settings map 而未持有读锁，
//     与并发的 Set() 会产生 data race（Go map 并发读写会 fatal crash）。
//     现在在读锁保护下构建一份键值快照，事务内部使用快照进行写入。
func SaveToDB(keys ...string) (err error) {
	if len(keys) == 0 {
		return nil
	}

	dbInstance := db.GetDB()
	if dbInstance == nil {
		return errors.New("systemsettings.SaveToDB: database is not initialized")
	}

	// 在读锁保护下拷贝待保存的键值快照，避免事务执行期间发生 data race。
	mu.RLock()
	snapshot := make(map[string]any, len(keys))
	for _, key := range keys {
		snapshot[key] = settings[key]
	}
	mu.RUnlock()

	rows := make([]models.SystemSetting, 0)

	err = dbInstance.Model(&models.SystemSetting{}).Where("`key` in (?)", keys).Find(&rows).Error
	if err != nil {
		err = errors.Wrapf(err, "failed to get system settings from db, keys: %v", keys)

		return err
	}

	existsKeyMap := make(map[string]bool)
	for _, row := range rows {
		existsKeyMap[row.Key] = true
	}

	err = dbInstance.Transaction(func(tx *gorm.DB) (err error) {
		for _, key := range keys {
			if _, ok := existsKeyMap[key]; ok {
				err = tx.Model(&models.SystemSetting{}).Where("`key` = ?", key).Update("val", snapshot[key]).Error
			} else {
				err = tx.Model(&models.SystemSetting{}).Create(&models.SystemSetting{
					Key: key,
					Val: snapshot[key],
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

		return err
	}

	return err
}

// GetAllFlatStyle 获取所有扁平化样式
// 例如： key a.b.c, val 1
// 返回： map[a.b.c]1.
//
// 返回内部缓存 map 的一份拷贝，避免调用方意外修改内部状态，
// 同时修复了旧实现中命名返回值遮蔽全局 settings 导致始终返回 nil 的 bug。
func GetAllFlatStyle() map[string]any {
	mu.RLock()
	defer mu.RUnlock()

	ret := make(map[string]any, len(settings))
	maps.Copy(ret, settings)

	return ret
}

// GetAllTreeStyle 获取所有树形样式
// 例如： key a.b.c, val 1
// 返回： map[a][b][c]1.
//
// 旧实现有两个 bug：
//  1. 循环中把 ret 重新指向子 map，导致后续 key 被构建到错误的子树中，
//     且最终返回的是某个深层子 map 而非根 map。
//  2. 使用不带 ok 的类型断言，当出现 key 冲突（如 a=1 和 a.b=2）时会 panic。
//
// 新实现使用独立的 cursor 指针遍历，每轮从根开始构建，
// 并使用安全的类型断言处理冲突。
func GetAllTreeStyle() map[string]any {
	mu.RLock()
	defer mu.RUnlock()

	root := make(map[string]any)
	keys := make([]string, 0, len(settings))

	for key := range settings {
		keys = append(keys, key)
	}

	// 把key 升序排序
	sort.Strings(keys)

	for _, key := range keys {
		val := settings[key]
		keyParts := strings.Split(key, ".")
		keyLen := len(keyParts)

		// 每轮都从根开始遍历，避免 cursor 被错误地重定向到子树
		cursor := root
		for i := range keyLen {
			if i == keyLen-1 {
				// 叶子节点：直接写入值
				cursor[keyParts[i]] = val
				break
			}

			// 中间节点：确保存在一个子 map
			existing, ok := cursor[keyParts[i]]
			if !ok {
				child := make(map[string]any)
				cursor[keyParts[i]] = child
				cursor = child
				continue
			}

			child, isMap := existing.(map[string]any)
			if !isMap {
				// key 冲突：已有标量值占用了该中间节点，用新的 map 覆盖它
				child = make(map[string]any)
				cursor[keyParts[i]] = child
			}

			cursor = child
		}
	}

	return root
}

func GetDefaultPageSize() int {
	return GetInt(KeyDefaultPageSize, 20)
}

// PlaceholderDomain is the value older versions seeded into the "domain"
// setting. It is never a real deployment domain.
const PlaceholderDomain = "example.com"

// configuredDomain returns the "domain" system setting, if set.
func configuredDomain() string {
	return strings.ToLower(strings.TrimSpace(GetString(KeyDomain, "")))
}

// GetDomain returns the local instance domain used for federation identities
// (actor URIs, acct:user@domain, Matrix server name).
//
// Priority: host of APP_BASE_URL when it is set explicitly, then the
// "domain" system setting, then the host of the default APP_BASE_URL
// (localhost:8080, development only).
func GetDomain() string {
	if config.AppBaseURLConfigured() {
		if host := config.AppBaseHost(); host != "" {
			return host
		}
	}

	if d := configuredDomain(); d != "" {
		return d
	}

	if host := config.AppBaseHost(); host != "" {
		return host
	}

	return "localhost"
}

// GetBaseURL returns the public base URL of the instance, with scheme and
// without a trailing slash, e.g. "https://social.example.org". Local status,
// actor and tag URLs are built from it.
func GetBaseURL() string {
	if config.AppBaseURLConfigured() && config.AppBaseHost() != "" {
		return config.C.AppBaseURL
	}

	if d := configuredDomain(); d != "" {
		return "https://" + d
	}

	if config.AppBaseHost() != "" {
		return config.C.AppBaseURL
	}

	return "http://localhost"
}

// CheckBaseURL validates the instance URL configuration at startup. It
// returns an error when federation identities would be wrong (production
// without APP_BASE_URL and without a real domain setting, or a malformed
// APP_BASE_URL) and a non-empty warning for weaker problems.
func CheckBaseURL(production bool) (warning string, err error) {
	if config.AppBaseURLConfigured() {
		u, perr := url.Parse(config.C.AppBaseURL)
		if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", errors.Errorf("APP_BASE_URL=%q must be an absolute http(s) URL such as https://social.example.org", config.C.AppBaseURL)
		}

		if d := configuredDomain(); d != "" && d != PlaceholderDomain && d != config.AppBaseHost() {
			warning = fmt.Sprintf("system setting domain=%q differs from APP_BASE_URL host %q; APP_BASE_URL wins", d, config.AppBaseHost())
		}

		return warning, nil
	}

	d := configuredDomain()
	if d == "" || d == PlaceholderDomain {
		msg := fmt.Sprintf("APP_BASE_URL is not set; federation URLs will use %s. Set APP_BASE_URL to the public URL of this instance (e.g. https://social.example.org)", GetBaseURL())
		if production {
			return "", errors.New(msg)
		}

		return msg, nil
	}

	return fmt.Sprintf("APP_BASE_URL is not set; using the domain system setting (%s) as https://%s", d, d), nil
}
