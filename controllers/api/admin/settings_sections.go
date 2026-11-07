package admin

import (
	"os"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/systemsettings"
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
		deploymentField("storage", "region", "区域", "text", "S3_REGION", ""),
		deploymentField("storage", "bucket", "存储桶", "text", "S3_BUCKET", ""),
		deploymentField("storage", "access_key", "访问密钥 ID", "secret", "S3_ACCESS_KEY", ""),
		deploymentField("storage", "secret_key", "访问密钥 Secret", "secret", "S3_SECRET_KEY", ""),
		deploymentField("storage", "use_ssl", "使用 HTTPS", "boolean", "S3_USE_SSL", true),
		deploymentField("storage", "base_url", "公开媒体基础 URL", "url", "S3_PUBLIC_URL", ""),
		deploymentField("storage", "path_style", "路径风格请求", "text", "S3_FORCE_PATH_STYLE", "auto"),
	}
	fields[0].Options = []string{"local", "s3", "minio"}
	fields[2].Description = "留空表示未设置。对象存储客户端在端点已填写且区域为空时使用 us-east-1；Cloudflare R2 填 auto。"
	fields[7].Description = "可选。指向存储桶根目录的公开地址（如 R2 公开桶 https://pub-xxxx.r2.dev 或自定义域名），不要在末尾加桶名；对象 media/a.jpg 将从 <该地址>/media/a.jpg 访问。若末尾仍是桶名且该路径访问不到对象、去掉后可以，重定向会改用较短的根地址。留空则由本站直接从存储桶读取并返回文件。"
	fields[8].Options = []string{"auto", "true", "false"}
	fields[8].Description = "true 使用 https://端点/桶名/对象 形式（R2、MinIO 推荐）；false 使用虚拟主机风格；auto 由客户端判断。"
	return fields
}

func mediaProcessingFields() []SettingsField {
	return []SettingsField{
		{Key: systemsettings.KeyMediaImageToWebP, Label: "图片转为 WebP", Type: "boolean", Value: true, Description: "开启后，新上传的本地图片一律完整解码并重新编码为 WebP，即使原文件已是 WebP 也不会复制码流。关闭后，输出由「强制重编码」和「清理隐私元数据」分别决定。保存本项不会改写其他开关。"},
		{Key: systemsettings.KeyMediaVideoToWebM, Label: "视频转为 WebM", Type: "boolean", Value: true, Description: "开启后，新上传的本地视频一律完整解码并重新编码为 WebM，即使原文件已是 WebM。关闭 WebM 并不能省掉重编码：强制重编码或隐私清理仍会完整解码再编码。并发、时长和分辨率上限用来限制 CPU。"},
		{Key: systemsettings.KeyMediaImageForceReencode, Label: "图片强制同格式重编码", Type: "boolean", Value: true, Description: "仅在「图片转为 WebP」关闭时改变输出：对 JPEG、PNG、GIF、WebP 做同格式完整重编码。转码开启时本项仍按保存的值保留，但输出格式由转码决定。关闭本项不会自动关闭隐私清理。"},
		{Key: systemsettings.KeyMediaVideoForceReencode, Label: "视频强制同格式重编码", Type: "boolean", Value: true, Description: "仅在「视频转为 WebM」关闭时改变输出：对 MP4、WebM、QuickTime 做同格式完整重编码。转码开启时本项仍按保存的值保留。关闭本项不会自动关闭隐私清理。"},
		{Key: systemsettings.KeyMediaStripPrivacy, Label: "清理隐私元数据", Type: "boolean", Value: true, Description: "开启时去掉 GPS、相机或设备型号、时间、作者、注释、EXIF/XMP 和内嵌预览。图片会先按方向写入像素并完成色彩转换，再丢弃配置文件。若转码和强制重编码都关闭，仍按原格式重写以完成清理，且不会因此打开强制重编码。关闭后，只有在不转码、不强制重编码时才原样保存，位置和设备信息可能仍在，保证更弱。凡是新生成的文件都使用元数据允许列表，关闭本项也不会把 GPS 复制进去。无法安全清理时拒绝该文件。"},
		{Key: systemsettings.KeyMediaImageQuality, Label: "图片质量", Type: "number", Value: systemsettings.DefaultImageQuality, Min: systemsettings.MinImageQuality, Max: systemsettings.MaxImageQuality, Description: "WebP 与同格式图片重编码的质量，整数 40–95，默认 80。数值越高体积越大、细节越多。"},
		{Key: systemsettings.KeyMediaVideoCRF, Label: "视频 CRF", Type: "number", Value: systemsettings.DefaultVideoCRF, Min: systemsettings.MinVideoCRF, Max: systemsettings.MaxVideoCRF, Description: "WebM（VP9）和同格式视频重编码（H.264）共用的 CRF，整数 20–50，默认 32。数值越高压缩越强、体积越小。"},
	}
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
