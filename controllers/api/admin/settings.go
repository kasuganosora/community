package admin

import (
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"

	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/systemsettings"
)

type SettingsField struct {
	Env       string   `json:"deployment_key,omitempty"`
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
		"branding": {Title: "品牌", Description: "站点缩略图用于站点公开介绍；不会修改账号头像或历史媒体。", Fields: []SettingsField{
			{Key: systemsettings.KeyThumbnail, Label: "站点缩略图 URL", Type: "url", Value: "", Max: 2048, Description: "仅支持 HTTPS 公开图片链接；保存不会主动下载该地址。"},
		}},
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
	sections["email"] = SettingsSection{Title: "邮件服务器", Description: "持久化配置将在服务器重启后用于新的邮件连接；环境变量优先。测试连接不会发送邮件。", Fields: emailSettingsFields()}
	sections["storage"] = SettingsSection{Title: "对象存储", Description: "修改存储连接需要重启服务器；不会自动迁移已有文件。保存后可测试已保存的目标配置，重启后才切换实际存储。", Fields: storageSettingsFields()}
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
		if field.Env != "" {
			envValue := deploymentSettingValue(field.Env)
			if config.IsExplicitlyConfigured(field.Env) {
				field.ReadOnly = true
				field.Source = "environment"
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
		}
		if _, exists := config.C.Other[field.Key]; exists {
			field.ReadOnly = true
			field.Source = "environment"
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
		if _, exists := config.C.Other[key]; exists || (field.Env != "" && config.IsExplicitlyConfigured(field.Env)) {
			c.JSON(http.StatusConflict, gin.H{"error": "setting is managed by deployment"})
			return
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
		if field.Type == "url" && v != "" {
			u, err := url.Parse(v)
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil {
				return invalid
			}
		}
	}
	return nil
}
