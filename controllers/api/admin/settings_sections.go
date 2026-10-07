package admin

import (
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"os"
)

func deploymentField(prefix, key, label, kind, env string, value any) SettingsField {
	return SettingsField{Key: "config." + prefix + "." + key, Label: label, Type: kind, Env: env, Value: value, RestartRequired: true, Max: 2048}
}
func emailSettingsFields() []SettingsField {
	fields := []SettingsField{
		deploymentField("email", "mode", "发送方式", "text", "EMAIL_MODE", "smtp"),
		deploymentField("email", "server", "SMTP 服务器", "text", "SMTP_SERVER", ""),
		deploymentField("email", "port", "SMTP 端口", "number", "SMTP_PORT", 587),
		deploymentField("email", "username", "SMTP 用户名", "text", "SMTP_USERNAME", ""),
		deploymentField("email", "password", "SMTP 密码", "secret", "SMTP_PASSWORD", ""),
		deploymentField("email", "from", "发件人邮箱", "email", "SMTP_FROM", ""),
		deploymentField("email", "auth_method", "认证方式", "text", "SMTP_AUTH_METHOD", "PLAIN"),
		deploymentField("email", "timeout", "连接超时（秒）", "number", "SMTP_TIMEOUT", 10),
		deploymentField("email", "tls", "隐式 TLS（通常使用 465 端口）", "boolean", "SMTP_TLS", true),
	}
	fields[0].Options = []string{"smtp"}
	fields[2].Min = 1
	fields[2].Max = 65535
	fields[6].Options = []string{"PLAIN", "LOGIN", "CRAM-MD5"}
	fields[7].Min = 1
	fields[7].Max = 60
	fields[8].Description = "开启使用隐式 TLS（通常为 465）；关闭使用 STARTTLS（通常为 587）。请按邮件服务器要求匹配端口。"
	return fields
}
func storageSettingsFields() []SettingsField {
	fields := []SettingsField{
		deploymentField("storage", "type", "存储类型", "text", "STORAGE_TYPE", "local"),
		deploymentField("storage", "endpoint", "S3 / MinIO 端点", "text", "S3_ENDPOINT", ""),
		deploymentField("storage", "region", "区域", "text", "S3_REGION", "us-east-1"),
		deploymentField("storage", "bucket", "存储桶", "text", "S3_BUCKET", "community"),
		deploymentField("storage", "access_key", "访问密钥 ID", "secret", "S3_ACCESS_KEY", ""),
		deploymentField("storage", "secret_key", "访问密钥 Secret", "secret", "S3_SECRET_KEY", ""),
		deploymentField("storage", "use_ssl", "使用 HTTPS", "boolean", "S3_USE_SSL", true),
		deploymentField("storage", "base_url", "公开媒体基础 URL", "url", "S3_PUBLIC_URL", ""),
	}
	fields[0].Options = []string{"local", "s3", "minio"}
	return fields
}
func databaseSettingsFields() []SettingsField {
	fields := []SettingsField{{Key: "database.driver", Label: "数据库类型", Type: "text", Value: config.C.DbConnection, ReadOnly: true}}
	if database := db.GetDB(); database != nil {
		if connection, err := database.DB(); err == nil {
			stats := connection.Stats()
			for _, f := range []struct {
				key, label string
				value      any
			}{{"open", "已打开连接", stats.OpenConnections}, {"in_use", "使用中连接", stats.InUse}, {"idle", "空闲连接", stats.Idle}, {"max_open", "最大连接数", stats.MaxOpenConnections}, {"wait_count", "累计等待次数", stats.WaitCount}, {"wait_duration", "累计等待时长", stats.WaitDuration.String()}} {
				fields = append(fields, SettingsField{Key: "database." + f.key, Label: f.label, Type: "text", Value: f.value, ReadOnly: true})
			}
		}
	}
	return fields
}

func deploymentSettingValue(env string) string {
	switch env {
	case "EMAIL_MODE":
		return config.C.EmailMode
	case "SMTP_SERVER":
		return config.C.SmtpServer
	case "SMTP_PORT":
		return config.C.SmtpPort
	case "SMTP_USERNAME":
		return config.C.SmtpUsername
	case "SMTP_PASSWORD":
		return config.C.SmtpPassword
	case "SMTP_FROM":
		return config.C.SmtpFrom
	case "SMTP_AUTH_METHOD":
		return config.C.SmtpAuthMethod
	case "SMTP_TIMEOUT":
		return config.C.SmtpTimeout
	case "SMTP_TLS":
		return config.C.SmtpTLS
	case "STORAGE_TYPE":
		return config.C.StorageType
	default:
		return config.C.Other[env]
	}
}

func externalSettingsFields() []SettingsField {
	fields := []SettingsField{
		deploymentField("search", "enabled", "Meilisearch 模式", "text", "MEILISEARCH_ENABLED", "auto"),
		deploymentField("search", "host", "Meilisearch 地址", "text", "MEILISEARCH_HOST", "http://127.0.0.1:7700"),
		deploymentField("search", "api_key", "Meilisearch API 密钥", "secret", "MEILISEARCH_API_KEY", ""),
		deploymentField("search", "index_prefix", "索引前缀", "text", "MEILISEARCH_INDEX_PREFIX", ""),
	}
	fields[0].Options = []string{"auto", "true", "false"}
	fields[0].Description = "auto 按搜索服务可用性选择；false 使用数据库搜索。修改后需重启，不会在保存时发起网络连接。"
	provider := os.Getenv("TRANSLATION_PROVIDER")
	if provider == "" {
		provider = "libretranslate"
	}
	fields = append(fields, SettingsField{Key: "translation.provider", Label: "翻译服务类型", Type: "text", Value: provider, ReadOnly: true, Description: "翻译连接由 TRANSLATION_PROVIDER 等部署环境变量管理；这里不提供无效的启用开关。"})
	fields = append(fields, SettingsField{Key: "translation.credentials", Label: "翻译 API 密钥配置", Type: "boolean", Value: os.Getenv("LIBRETRANSLATE_API_KEY") != "" || os.Getenv("DEEPL_API_KEY") != "" || os.Getenv("GOOGLE_TRANSLATE_API_KEY") != "", ReadOnly: true, Description: "仅显示是否存在凭据，不代表服务已验证可用。"})
	return fields
}
