package config

import (
	"github.com/joho/godotenv"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/utils"
	"os"
	"reflect"
	"strings"
)

type Config struct {
	Listen                  string `default:":8080"                 env:"LISTEN"`
	AppBaseURL              string `default:"http://localhost:8080" env:"APP_BASE_URL"`
	StorageType             string `default:"local"                 env:"STORAGE_TYPE"`
	EncryptedKey            string `env:"ENCRYPTED_KEY"` // 加密密钥, 必须通过环境变量配置
	DbConnection            string `default:"mysql"                 env:"DB_CONNECTION"`
	DbHost                  string `env:"DB_HOST"`
	DbPort                  string `default:"3306"                  env:"DB_PORT"`
	DbDatabase              string `default:"community"             env:"DB_DATABASE"`
	DbUsername              string `default:"root"                  env:"DB_USERNAME"`
	DbPassword              string `default:""                      env:"DB_PASSWORD"`
	HttpProxy               string `env:"HTTP_PROXY"`
	SmtpServer              string `env:"SMTP_SERVER"`
	SmtpPort                string `env:"SMTP_PORT"`
	SmtpUsername            string `env:"SMTP_USERNAME"`
	SmtpPassword            string `env:"SMTP_PASSWORD"`
	SmtpFrom                string `env:"SMTP_FROM"`
	SmtpAuthMethod          string `default:"PLAIN"                 env:"SMTP_AUTH_METHOD"`
	SmtpTimeout             string `default:"10"                    env:"SMTP_TIMEOUT"`
	SmtpTLS                 string `default:"true"                  env:"SMTP_TLS"` // 是否启用 TLS 加密传输
	EmailMode               string `default:"smtp"                  env:"EMAIL_MODE"`
	DevMode                 string `default:"false"                 env:"DEV_MODE"`       // 开发模式,如果是,则从本地加载内嵌的静态文件(resources文件夹下的文件)
	SessionExpire           string `default:"1382400"               env:"SESSION_EXPIRE"` // Session 过期时间, 默认 16 天
	Redis0Host              string `default:""                      env:"REDIS_0_HOST"`   // Redis 配置 0, 多个 Redis 请依次递增
	Redis0Port              string `default:"6379"                  env:"REDIS_0_PORT"`
	Redis0Password          string `default:""                      env:"REDIS_0_PASSWORD"`
	Redis0Db                string `default:"0"                     env:"REDIS_0_DB"`
	WebSocketAllowedOrigins string `env:"WEBSOCKET_ALLOWED_ORIGINS"`
	CORSAllowedOrigins      string `env:"CORS_ALLOWED_ORIGINS"` // 逗号分隔的允许来源列表，为空则允许所有（仅开发模式）

	// 其他配置
	Other map[string]string `env:"-"` // 不在上面的配置中的配置项
}

var C Config

// isTestMode returns true when running under `go test`.
// Detected via the well-known TEST_MODE env var (set by TestMain) or
// by checking if the process binary name contains "test".
func isTestMode() bool {
	if os.Getenv("TEST_MODE") == "1" {
		return true
	}
	// Check if binary name looks like a test binary
	if strings.HasSuffix(os.Args[0], ".test") || strings.Contains(os.Args[0], "/_test/") {
		return true
	}
	return false
}

func init() {
	C.Other = make(map[string]string)

	loadConfig()
}

func loadConfig() {
	// 取得当前运行目录下有没有 .env 文件，如果有则通过他来设置配置
	envFilePath := utils.GetFullPath(".env")

	var err error

	envFileExists := utils.IsFileExists(envFilePath)
	if envFileExists {
		err = loadConfigFromEnvFile(envFilePath)
	} else {
		err = loadConfigFromEnv()
	}

	if (err != nil && !envFileExists) || (!envFileExists && C.DbHost == "") {
		if createErr := createDefaultEnvFile(envFilePath); createErr != nil {
			log.Error("Failed to create default .env file: %v", createErr)
		}

		os.Exit(1)
	} else if err != nil {
		panic(err)
	}

	// In test mode, skip mandatory checks — tests don't need real DB/encryption.
	// Production startup still enforces all checks below.
	if isTestMode() {
		// If no key at all, provide a dummy test key so downstream code doesn't panic.
		if C.EncryptedKey == "" {
			C.EncryptedKey = "test-mode-dummy-key-0000000000000000"
		}
		return
	}

	// 校验加密密钥
	if C.EncryptedKey == "" {
		log.Fatal("ENCRYPTED_KEY is not set. Please configure it in .env file or environment variable.")
	}

	if len(C.EncryptedKey) < 16 {
		log.Fatal("ENCRYPTED_KEY must be at least 16 characters long for security.")
	}

	// 生产环境校验数据库密码
	if C.DevMode != "true" && C.DbPassword == "" {
		log.Fatal("DB_PASSWORD is not set. Please configure it in .env file or environment variable. (In development mode, set DEV_MODE=true to bypass this check.)")
	}
}

// loadConfigFromEnv 从环境变量中加载配置.
func loadConfigFromEnv() (err error) { //nolint:unparam // param reserved for future use
	envData := make(map[string]string)

	for _, env := range os.Environ() {
		envPair := strings.SplitN(env, "=", 2)
		if len(envPair) < 2 {
			continue
		}

		envData[envPair[0]] = envPair[1]
	}

	loadMapToConfig(envData)

	return
}

// loadConfigFromEnvFile 从 .env 文件中加载配置.
func loadConfigFromEnvFile(envPath string) (err error) {
	fp, err := os.Open(envPath) //nolint:gosec // G304: reviewed and safe
	if err != nil {
		return
	}
	defer func() { _ = fp.Close() }()

	envData, err := godotenv.Parse(fp)
	if err != nil {
		return
	}

	loadMapToConfig(envData)

	return
}

// loadMapToConfig 将 map 中的数据加载到 Config 中.
func loadMapToConfig(data map[string]string) {
	sType := reflect.TypeFor[Config]()
	sValue := reflect.ValueOf(&C).Elem()

	standardConfigsKeyMap := make(map[string]bool)

	for i := range sType.NumField() {
		field := sType.Field(i)
		value := sValue.Field(i)
		envName, ok := field.Tag.Lookup("env")

		// 如果没有 env 标签则跳过
		if !ok {
			continue
		}

		envName = strings.ToUpper(envName)
		standardConfigsKeyMap[envName] = true

		if envValue, envExists := data[envName]; envExists && envValue != "" {
			// Only use .env value if non-empty; empty values should not override
			// defaults or actual environment variables.
			value.SetString(envValue)
		} else {
			// 不存在则尝试使用默认值
			if defaultValue, defaultValExists := field.Tag.Lookup("default"); defaultValExists {
				value.SetString(defaultValue)
			}
		}
	}

	// 把不在 standardConfigsKeyMap 中的配置项放到 Other 中
	for key, value := range data {
		if _, ok := standardConfigsKeyMap[key]; ok {
			continue
		}

		C.Other[key] = value
	}
}

// createDefaultEnvFile 创建默认的 .env 文件.
func createDefaultEnvFile(path string) (err error) {
	log.Info("Create default .env file")

	sType := reflect.TypeFor[Config]()
	sValue := reflect.ValueOf(&C).Elem()

	fp, err := os.Create(path) //nolint:gosec // G304: reviewed and safe
	if err != nil {
		return err
	}

	defer func() { _ = fp.Close() }()

	for i := range sType.NumField() {
		field := sType.Field(i)
		value := sValue.Field(i)
		envName, ok := field.Tag.Lookup("env")

		if !ok {
			continue
		}

		envName = strings.ToUpper(envName)
		if envName == "-" {
			continue
		}

		if defaultValue, defaultValExists := field.Tag.Lookup("default"); defaultValExists {
			_, err = fp.WriteString(envName + "=" + defaultValue + "\n")
		} else {
			_, err = fp.WriteString(envName + "=" + value.String() + "\n")
		}

		if err != nil {
			return err
		}
	}

	return err
}
