package admin

import (
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"slices"
	"sort"
	"strconv"

	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/runtimeconfig"
	"github.com/kasuganosora/community/systemsettings"
)

type SettingsField struct {
	Env string `json:"deployment_key,omitempty"`
	// LockedBy tells where a deployment value comes from: "env_file" (the
	// .env file) or "process" (the container/process environment).
	LockedBy  string   `json:"locked_by,omitempty"`
	SecretSet bool     `json:"secret_set,omitempty"`
	Options   []string `json:"options,omitempty"`

	Key             string `json:"key"`
	Label           string `json:"label"`
	Type            string `json:"type"`
	Value           any    `json:"value,omitempty"`
	Description     string `json:"description,omitempty"`
	ReadOnly        bool   `json:"read_only"`
	Source          string `json:"source"`
	RestartRequired bool   `json:"restart_required"`
	Min             int    `json:"min,omitempty"`
	Max             int    `json:"max,omitempty"`
}
type SettingsSection struct {
	Section     string          `json:"section"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Fields      []SettingsField `json:"fields"`
}

func settingsSections() map[string]SettingsSection {
	sections := map[string]SettingsSection{
		"general": {Title: "常规", Description: "修改站点公开信息；保存后新请求立即生效。", Fields: []SettingsField{
			{Key: systemsettings.KeyTitle, Label: "站点名称", Type: "text", Value: "Community", Max: 100},
			{Key: systemsettings.KeyDescription, Label: "站点介绍", Type: "textarea", Value: "", Max: 5000},
			{Key: systemsettings.KeySiteContactEmail, Label: "联系邮箱", Type: "email", Value: "", Max: 254},
		}},
		"branding": {Title: "品牌", Description: "站点图标、图片、主题色与入口页样式，与 Misskey 的「品牌」设置对应；不会修改账号头像或历史媒体。", Fields: append([]SettingsField{
			{Key: systemsettings.KeyThumbnail, Label: "站点缩略图 URL", Type: "url", Value: "", Max: 2048, Description: "仅支持 HTTPS 公开图片链接；保存不会主动下载该地址。"},
		}, brandingSettingsFields()...)},
		"moderation": {Title: "注册管理", Description: "控制新的注册请求；不会更改已有账号状态。", Fields: []SettingsField{
			{Key: systemsettings.KeyIsEnableRegistration, Label: "允许注册", Type: "boolean", Value: true},
			{Key: systemsettings.KeyRegistrationApprovalRequired, Label: "注册需要管理员审核", Type: "boolean", Value: false},
			{Key: systemsettings.KeyRegistrationInviteRequired, Label: "注册需要邀请码", Type: "boolean", Value: false},
			{Key: systemsettings.KeyRegistrationCheckEmail, Label: "注册需要邮箱验证", Type: "boolean", Value: false, Description: "启用前请确认邮件发送可用。"},
			{Key: systemsettings.KeyRegistrationMessage, Label: "注册提示", Type: "textarea", Value: "", Max: 2000},
		}},
		"performance": {Title: "性能", Description: "仅提供有范围限制的运行参数；数据库连接池和缓存部署由服务器环境管理。", Fields: []SettingsField{
			{Key: systemsettings.KeyDefaultPageSize, Label: "默认每页条数", Type: "number", Value: 20, Min: 1, Max: 80, Description: "影响未指定 limit 的分页请求，不改变接口各自的最大上限。"},
			{Key: systemsettings.KeyRecommendationEnabled, Label: "启用推荐时间线与关注建议", Type: "boolean", Value: false, Description: "默认关闭；开启后按公开资格和用户发现偏好推荐内容。关闭会使已有推荐翻页失效。"},
			{Key: systemsettings.KeyRecommendationMode, Label: "推荐算法", Type: "select", Value: "auto", Options: []string{"auto", "rules"}, Max: 16, Description: "auto 在索引就绪后使用 Meilisearch（开启人工审核时还需批准版本），否则使用规则排序。rules 固定使用规则排序。"},
			{Key: systemsettings.KeyRecommendationManualApproval, Label: "需要人工审核推荐索引", Type: "boolean", Value: false, Description: "默认关闭：索引构建完成且健康（非空、未过期、配置一致）后自动启用 Meilisearch 增强推荐。开启后，每个新索引版本都需要管理员在“推荐管理”页点击“批准当前版本”才会生效，未批准前使用规则排序。"},
			{Key: systemsettings.KeyRecommendationApprovedVersion, Label: "已批准的索引版本（高级）", Type: "text", Value: "", Max: 128, Description: "仅在开启“需要人工审核推荐索引”时使用。通常无需手动填写，请在“推荐管理”页点击“批准当前版本”自动写入。"},
			{Key: systemsettings.KeyDiscoveryTrendsEnabled, Label: "启用热门话题", Type: "boolean", Value: true, Description: "独立控制公开热点和趋势接口，不受用户推荐偏好影响。"},
		}},
	}
	sections["api_tokens"] = SettingsSection{Title: "API 令牌", Description: "访问令牌的安全策略。", Fields: []SettingsField{
		{Key: systemsettings.KeyAPITokenInactivityExpiry, Label: "自动吊销 6 个月未使用的 API 令牌", Type: "boolean", Value: false, Description: "开启后，最后一次使用（从未使用则为创建时间）超过 180 天的访问令牌会在下次使用时被吊销。"},
	}}
	sections["email"] = SettingsSection{Title: "邮件服务器", Description: "保存后立即用于新的邮件连接。数据库中的值优先；只有该配置还没保存过时，才使用已有的环境变量。测试连接不会发送邮件。", Fields: emailSettingsFields()}
	sections["storage"] = SettingsSection{Title: "对象存储", Description: "保存后立即切换存储连接，不必重启；不会自动迁移已有文件。数据库中的值优先；只有该配置还没保存过时，才使用已有的环境变量。未填写的存储桶会保持空白。", Fields: storageSettingsFields()}
	sections["database"] = SettingsSection{Title: "数据库", Description: "数据库连接由部署环境管理，这里仅展示当前连接池诊断，不提供 SQL 执行或凭据。", Fields: databaseSettingsFields()}
	sections["external"] = SettingsSection{Title: "外部服务", Description: "管理实际使用的搜索集成，查看翻译服务部署配置。", Fields: externalSettingsFields()}
	return sections
}
func GetSettings(c *gin.Context) {
	section, ok := settingsSections()[c.Param("section")]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown settings section"})
		return
	}
	section.Section = c.Param("section")
	for i := range section.Fields {
		field := &section.Fields[i]
		if !field.ReadOnly {
			field.Value = systemsettings.Get(field.Key, field.Value)
		}
		field.Source = "database"
		if field.ReadOnly {
			field.Source = "runtime"
		}
		if field.Env != "" && config.IsExplicitlyConfigured(field.Env) {
			envValue := deploymentSettingValue(field.Env)
			_, stored := systemsettings.Stored(field.Key)
			if dbBackedSections[section.Section] {
				// Show the legacy environment value until an admin saves, which
				// writes the database and then takes precedence.
				if !stored {
					applyDeploymentDisplay(field, envValue)
					if field.Description != "" {
						field.Description += " "
					}
					field.Description += "当前显示的是环境变量中的旧值；保存后写入数据库并立即生效。"
				}
			} else {
				field.ReadOnly = true
				field.Source = "environment"
				field.LockedBy = "process"
				if config.IsSetInEnvFile(field.Env) {
					field.LockedBy = "env_file"
				}
				applyDeploymentDisplay(field, envValue)
			}
		}
		if _, exists := config.C.Other[field.Key]; exists && !dbBackedSections[section.Section] {
			field.ReadOnly = true
			field.Source = "environment"
			field.LockedBy = "process"
			if config.IsSetInEnvFile(field.Key) {
				field.LockedBy = "env_file"
			}
		}
		if liveSections[section.Section] && field.Env != "" {
			field.RestartRequired = false
		}
		if field.Type == "secret" {
			field.SecretSet = fmt.Sprint(field.Value) != "" && field.Value != nil
			field.Value = nil
		}
	}
	c.JSON(http.StatusOK, section)
}
func UpdateSettings(c *gin.Context) {
	section, ok := settingsSections()[c.Param("section")]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown settings section"})
		return
	}
	var req struct {
		Values       map[string]any `json:"values"`
		ClearSecrets []string       `json:"clear_secrets"`
	}
	if c.ShouldBindJSON(&req) != nil || (len(req.Values) == 0 && len(req.ClearSecrets) == 0) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "values required"})
		return
	}
	allowed := map[string]SettingsField{}
	for _, field := range section.Fields {
		allowed[field.Key] = field
	}
	if req.Values == nil {
		req.Values = map[string]any{}
	}
	for _, key := range req.ClearSecrets {
		field, ok := allowed[key]
		if !ok || field.Type != "secret" {
			c.JSON(422, gin.H{"error": "only secret fields can be cleared"})
			return
		}
		if _, exists := req.Values[key]; exists {
			c.JSON(422, gin.H{"error": "cannot replace and clear the same secret"})
			return
		}
		req.Values[key] = ""
	}
	for key, value := range req.Values {
		field, ok := allowed[key]
		if !ok || field.ReadOnly {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "unknown or non-editable setting"})
			return
		}
		if !dbBackedSections[c.Param("section")] {
			if _, exists := config.C.Other[key]; exists || (field.Env != "" && config.IsExplicitlyConfigured(field.Env)) {
				c.JSON(http.StatusConflict, gin.H{"error": "setting is managed by deployment"})
				return
			}
		}

		if field.Type == "secret" && value == "" {
			explicitClear := false
			for _, keyToClear := range req.ClearSecrets {
				if keyToClear == key {
					explicitClear = true
				}
			}
			if !explicitClear {
				c.JSON(422, gin.H{"error": "omit unchanged secrets or use clear_secrets"})
				return
			}
		}
		if err := validateSettingsValue(field, value); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
			return
		}
	}
	if err := systemsettings.SaveChanges(req.Values); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "settings could not be saved"})
		return
	}
	auditSettingsChange(c, c.Param("section"), allowed, req.Values, req.ClearSecrets)
	switch c.Param("section") {
	case "email":
		runtimeconfig.ApplyEmail()
	case "storage":
		runtimeconfig.ApplyStorage()
	}
	GetSettings(c)
}
func validateSettingsValue(field SettingsField, value any) error {
	invalid := fmt.Errorf("invalid value for %s", field.Key)
	switch field.Type {
	case "boolean":
		if _, ok := value.(bool); !ok {
			return invalid
		}
	case "number":
		v, ok := value.(float64)
		if !ok || v < float64(field.Min) || v > float64(field.Max) || v != float64(int(v)) {
			return invalid
		}
	default:
		v, ok := value.(string)
		if !ok || utf8.RuneCountInString(v) > field.Max {
			return invalid
		}
		if len(field.Options) > 0 {
			found := false
			for _, option := range field.Options {
				if option == v {
					found = true
				}
			}
			if !found {
				return invalid
			}
		}
		if field.Key == systemsettings.KeyTitle && strings.TrimSpace(v) == "" {
			return invalid
		}
		if field.Type == "email" && v != "" {
			a, err := mail.ParseAddress(v)
			if err != nil || a.Address != v {
				return invalid
			}
		}
		if field.Type == "color" && v != "" && !systemsettings.ValidThemeColor(v) {
			return invalid
		}
		if field.Type == "json" {
			check := systemsettings.ValidateJSONObject
			if field.Key == systemsettings.KeyBrandDefaultLightTheme || field.Key == systemsettings.KeyBrandDefaultDarkTheme {
				check = systemsettings.ValidateTheme
			}
			if check(v) != nil {
				return invalid
			}
		}
		if field.Type == "url" && v != "" {
			u, err := url.Parse(v)
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
				return invalid
			}
		}
	}
	return nil
}

func brandingSettingsFields() []SettingsField {
	img := func(key, label, desc string) SettingsField {
		return SettingsField{Key: key, Label: label, Type: "url", Value: "", Max: 2048, Description: desc}
	}
	themeDesc := "Misskey 主题代码（JSON）。作为全站默认主题应用到所有访客；props 中 accent、bg、fg、panel、divider、link 等纯色值会映射到界面配色，引用其他属性的写法会被忽略。留空使用内置主题。"
	return []SettingsField{
		{Key: systemsettings.KeyBrandEntrancePageStyle, Label: "入口页样式", Type: "select", Value: "classic", Options: []string{"classic", "simple"}, Max: 16, Description: "classic：经典入口页（站点介绍、横幅与时间线）；simple：简洁入口页（仅图标、名称与登录注册）。"},
		{Key: systemsettings.KeyBrandShowTimelineVisitor, Label: "在入口页向访客显示时间线", Type: "boolean", Value: true},
		{Key: systemsettings.KeyBrandShowActivitiesVisitor, Label: "在入口页向访客显示站点活跃度", Type: "boolean", Value: true},
		img(systemsettings.KeyBrandIconURL, "图标 URL", "用作网站图标（favicon）及远程实例显示的本站图标。"),
		img(systemsettings.KeyBrandApp192IconURL, "图标 URL（App / 192px）", "用于 PWA 清单与 apple-touch-icon。建议 192×192 PNG，图案位于中间安全区域、背景不透明。"),
		img(systemsettings.KeyBrandApp512IconURL, "图标 URL（App / 512px）", "用于 PWA 清单。建议 512×512 PNG，图案位于中间安全区域、背景不透明。"),
		img(systemsettings.KeyBrandBannerURL, "横幅图片 URL", "显示在入口页与关于页顶部。"),
		img(systemsettings.KeyBrandBackgroundImageURL, "背景图片 URL", "显示在入口页背景。"),
		img(systemsettings.KeyBrandNotFoundImageURL, "404 页面图片 URL", "页面不存在时显示的插图。"),
		img(systemsettings.KeyBrandInfoImageURL, "提示图片 URL", "列表为空等提示状态中显示的插图。"),
		img(systemsettings.KeyBrandServerErrorImageURL, "错误页面图片 URL", "页面加载出错时显示的插图。"),
		{Key: systemsettings.KeyBrandThemeColor, Label: "主题色", Type: "color", Value: "", Max: 9, Description: "十六进制颜色，例如 #86b300。用于浏览器 theme-color、PWA 清单，并通过 NodeInfo 提供给其他实例显示实例标签颜色。"},
		{Key: systemsettings.KeyBrandDefaultLightTheme, Label: "默认浅色主题", Type: "json", Value: "", Max: 32768, Description: themeDesc},
		{Key: systemsettings.KeyBrandDefaultDarkTheme, Label: "默认深色主题", Type: "json", Value: "", Max: 32768, Description: themeDesc},
		{Key: systemsettings.KeyBrandRepositoryURL, Label: "仓库 URL", Type: "url", Value: "", Max: 2048, Description: "服务器软件源代码仓库地址，显示在关于页并写入 NodeInfo。留空使用默认仓库。"},
		{Key: systemsettings.KeyBrandFeedbackURL, Label: "反馈 URL", Type: "url", Value: "", Max: 2048, Description: "问题反馈地址，显示在关于页。"},
		{Key: systemsettings.KeyBrandManifestJSONOverride, Label: "manifest.json 覆盖", Type: "json", Value: "", Max: 32768, Description: "JSON 对象，顶层字段会覆盖站点提供的 manifest.json 中的同名字段。"},
	}
}

// liveSections are applied to the running server on save (no restart).
var liveSections = map[string]bool{"email": true, "storage": true}

// dbBackedSections are stored in system_settings and stay editable. An
// existing environment value is only a fallback until the key is saved.
var dbBackedSections = map[string]bool{"email": true, "storage": true}

func applyDeploymentDisplay(field *SettingsField, envValue string) {
	switch field.Type {
	case "number":
		n, _ := strconv.Atoi(envValue)
		field.Value = n
	case "boolean":
		field.Value = envValue == "true"
	default:
		field.Value = envValue
	}
}

// auditSettingsChange records which settings changed in the request's
// administrative audit entry. Values are recorded for plain settings only;
// secrets are marked "(changed)" / "(cleared)" and never written.
func auditSettingsChange(c *gin.Context, section string, fields map[string]SettingsField, values map[string]any, cleared []string) {
	id, _ := c.Get("admin_audit_id")
	auditID, ok := id.(uint64)
	if !ok || auditID == 0 {
		return
	}
	details := map[string]string{"section": section}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		field := fields[key]
		switch {
		case field.Type == "secret" && slices.Contains(cleared, key):
			details[key] = "(cleared)"
		case field.Type == "secret":
			details[key] = "(changed)"
		default:
			v := fmt.Sprint(values[key])
			if utf8.RuneCountInString(v) > 200 {
				v = string([]rune(v)[:200]) + "…"
			}
			details[key] = v
		}
	}
	entry := models.AdminAuditLog{Details: details}
	if err := db.GetDB().WithContext(c.Request.Context()).Model(&models.AdminAuditLog{}).Where("id = ?", auditID).Select("Details").Updates(&entry).Error; err != nil {
		log.Warn("Could not record settings audit details for entry %d", auditID)
	}
}
