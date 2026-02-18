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
	Listen         string `env:"LISTEN" default:":8080"`
	AppBaseURL     string `env:"APP_BASE_URL" default:"http://localhost:8080"`
	StorageType    string `env:"STORAGE_TYPE" default:"local"`
	EncryptedKey   string `env:"ENCRYPTED_KEY" default:"zwKc2UD3Nn8QbkypUkDbpJNyTmKeDbFbUiAGhAArpDa"` // 加密密钥, 如果空则不加密
	DbConnection   string `env:"DB_CONNECTION" default:"mysql"`
	DbHost         string `env:"DB_HOST"`
	DbPort         string `env:"DB_PORT" default:"3306"`
	DbDatabase     string `env:"DB_DATABASE" default:"community"`
	DbUsername     string `env:"DB_USERNAME" default:"root"`
	DbPassword     string `env:"DB_PASSWORD" default:""`
	HttpProxy      string `env:"HTTP_PROXY"`
	SmtpServer     string `env:"SMTP_SERVER"`
	SmtpPort       string `env:"SMTP_PORT"`
	SmtpUsername   string `env:"SMTP_USERNAME"`
	SmtpPassword   string `env:"SMTP_PASSWORD"`
	SmtpFrom       string `env:"SMTP_FROM"`
	SmtpAuthMethod string `env:"SMTP_AUTH_METHOD" default:"PLAIN"`
	SmtpTimeout    string `env:"SMTP_TIMEOUT" default:"10"`
	EmailMode      string `env:"EMAIL_MODE" default:"smtp"`
	DevMode        string `env:"DEV_MODE" default:"false"`         // 开发模式,如果是,则从本地加载内嵌的静态文件(resources文件夹下的文件)
	SessionExpire  string `env:"SESSION_EXPIRE" default:"1382400"` // Session 过期时间, 默认 16 天
	Redis0Host     string `env:"REDIS_0_HOST" default:""`          // Redis 配置 0, 多个 Redis 请依次递增
	Redis0Port     string `env:"REDIS_0_PORT" default:"6379"`
	Redis0Password string `env:"REDIS_0_PASSWORD" default:""`
	Redis0Db       string `env:"REDIS_0_DB" default:"0"`

	// 其他配置
	Other map[string]string `env:"-"` // 不在上面的配置中的配置项
}

var C Config

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
		createDefaultEnvFile(envFilePath)
		os.Exit(1)
	} else if err != nil {
		panic(err)
	}
}

// loadConfigFromEnv 从环境变量中加载配置
func loadConfigFromEnv() (err error) {
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

// loadConfigFromEnvFile 从 .env 文件中加载配置
func loadConfigFromEnvFile(envPath string) (err error) {
	fp, err := os.Open(envPath)
	if err != nil {
		return
	}
	defer fp.Close()

	envData, err := godotenv.Parse(fp)

	if err != nil {
		return
	}

	loadMapToConfig(envData)

	return
}

// loadMapToConfig 将 map 中的数据加载到 Config 中
func loadMapToConfig(data map[string]string) {
	sType := reflect.TypeOf(C)
	sValue := reflect.ValueOf(&C).Elem()

	standardConfigsKeyMap := make(map[string]bool)

	for i := 0; i < sType.NumField(); i++ {
		field := sType.Field(i)
		value := sValue.Field(i)
		envName, ok := field.Tag.Lookup("env")

		// 如果没有 env 标签则跳过
		if !ok {
			continue
		}

		envName = strings.ToUpper(envName)
		standardConfigsKeyMap[envName] = true

		if envValue, envExists := data[envName]; envExists {
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

	return
}

// createDefaultEnvFile 创建默认的 .env 文件
func createDefaultEnvFile(path string) (err error) {
	log.Info("Create default .env file")
	sType := reflect.TypeOf(C)
	sValue := reflect.ValueOf(&C).Elem()

	fp, err := os.Create(path)

	if err != nil {
		return
	}
	defer fp.Close()

	for i := 0; i < sType.NumField(); i++ {
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
			return
		}
	}

	return
}
