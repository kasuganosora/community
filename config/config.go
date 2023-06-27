package config

import (
	"github.com/joho/godotenv"
	"github.com/kasuganosora/community/utils"
	"os"
	"reflect"
	"strings"
)

type Config struct {
	DSN    string `env:"DSN"`
	Listen string `env:"LISTEN" default:":8080"`
	Host   string `env:"HOST" default:"localhost"`
}

var C Config

func init() {
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

	if err != nil && !envFileExists {
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

	for i := 0; i < sType.NumField(); i++ {
		field := sType.Field(i)
		value := sValue.Field(i)
		envName, ok := field.Tag.Lookup("env")

		// 如果没有 env 标签则跳过
		if !ok {
			continue
		}

		envName = strings.ToUpper(envName)

		if envValue, envExists := data[envName]; envExists {
			value.SetString(envValue)
		} else {
			// 不存在则尝试使用默认值
			if defaultValue, defaultValExists := field.Tag.Lookup("default"); defaultValExists {
				value.SetString(defaultValue)
			}
		}
	}

	return
}

// createDefaultEnvFile 创建默认的 .env 文件
func createDefaultEnvFile(path string) (err error) {
	sType := reflect.TypeOf(C)
	sValue := reflect.ValueOf(&C).Elem()

	fp, err := os.Create(path)

	if err != nil {
		return
	}

	for i := 0; i < sType.NumField(); i++ {
		field := sType.Field(i)
		value := sValue.Field(i)
		envName, ok := field.Tag.Lookup("env")

		if !ok {
			continue
		}

		envName = strings.ToUpper(envName)

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
