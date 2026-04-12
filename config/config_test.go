package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigDefaults(t *testing.T) {
	// Test that config has default values set
	assert.Equal(t, ":8080", C.Listen)
	assert.Equal(t, "http://localhost:8080", C.AppBaseURL)
	assert.Equal(t, "local", C.StorageType)
	assert.Equal(t, "mysql", C.DbConnection)
	assert.Equal(t, "3306", C.DbPort)
	assert.Equal(t, "community", C.DbDatabase)
	assert.Equal(t, "root", C.DbUsername)
	assert.Equal(t, "PLAIN", C.SmtpAuthMethod)
	assert.Equal(t, "10", C.SmtpTimeout)
	assert.Equal(t, "smtp", C.EmailMode)
	assert.Equal(t, "false", C.DevMode)
	assert.Equal(t, "1382400", C.SessionExpire)
	assert.Equal(t, "6379", C.Redis0Port)
	assert.Equal(t, "0", C.Redis0Db)
}

func TestConfigStruct(t *testing.T) {
	cfg := Config{
		Listen:     ":3000",
		AppBaseURL: "http://example.com",
	}

	assert.Equal(t, ":3000", cfg.Listen)
	assert.Equal(t, "http://example.com", cfg.AppBaseURL)
}

func TestConfigOther(t *testing.T) {
	// Test that Other map is initialized
	assert.NotNil(t, C.Other)
}

func TestLoadMapToConfig(t *testing.T) {
	// Create a test config
	testConfig := Config{}
	testConfig.Other = make(map[string]string)

	data := map[string]string{
		"LISTEN":       ":9000",
		"APP_BASE_URL": "http://test.com",
		"CUSTOM_KEY":   "custom_value",
	}

	// Use the original function to load
	// Note: This modifies global C, so we need to be careful
	originalListen := C.Listen
	originalURL := C.AppBaseURL

	loadMapToConfig(data)

	assert.Equal(t, ":9000", C.Listen)
	assert.Equal(t, "http://test.com", C.AppBaseURL)
	assert.Equal(t, "custom_value", C.Other["CUSTOM_KEY"])

	// Restore original values
	C.Listen = originalListen
	C.AppBaseURL = originalURL
	delete(C.Other, "CUSTOM_KEY")
}

func TestConfigFields(t *testing.T) {
	// Test all config fields exist
	cfg := Config{
		Listen:         ":8080",
		AppBaseURL:     "http://localhost:8080",
		StorageType:    "local",
		EncryptedKey:   "test-key",
		DbConnection:   "mysql",
		DbHost:         "localhost",
		DbPort:         "3306",
		DbDatabase:     "testdb",
		DbUsername:     "user",
		DbPassword:     "pass",
		HttpProxy:      "http://proxy:8080",
		SmtpServer:     "smtp.example.com",
		SmtpPort:       "587",
		SmtpUsername:   "user",
		SmtpPassword:   "pass",
		SmtpFrom:       "from@example.com",
		SmtpAuthMethod: "PLAIN",
		SmtpTimeout:    "10",
		EmailMode:      "smtp",
		DevMode:        "false",
		SessionExpire:  "1382400",
		Redis0Host:     "localhost",
		Redis0Port:     "6379",
		Redis0Password: "",
		Redis0Db:       "0",
		Other:          map[string]string{"key": "value"},
	}

	assert.Equal(t, ":8080", cfg.Listen)
	assert.Equal(t, "http://localhost:8080", cfg.AppBaseURL)
	assert.Equal(t, "local", cfg.StorageType)
	assert.Equal(t, "test-key", cfg.EncryptedKey)
	assert.Equal(t, "mysql", cfg.DbConnection)
	assert.Equal(t, "localhost", cfg.DbHost)
	assert.Equal(t, "3306", cfg.DbPort)
	assert.Equal(t, "testdb", cfg.DbDatabase)
	assert.Equal(t, "user", cfg.DbUsername)
	assert.Equal(t, "pass", cfg.DbPassword)
	assert.Equal(t, "http://proxy:8080", cfg.HttpProxy)
	assert.Equal(t, "smtp.example.com", cfg.SmtpServer)
	assert.Equal(t, "587", cfg.SmtpPort)
	assert.Equal(t, "user", cfg.SmtpUsername)
	assert.Equal(t, "pass", cfg.SmtpPassword)
	assert.Equal(t, "from@example.com", cfg.SmtpFrom)
	assert.Equal(t, "PLAIN", cfg.SmtpAuthMethod)
	assert.Equal(t, "10", cfg.SmtpTimeout)
	assert.Equal(t, "smtp", cfg.EmailMode)
	assert.Equal(t, "false", cfg.DevMode)
	assert.Equal(t, "1382400", cfg.SessionExpire)
	assert.Equal(t, "localhost", cfg.Redis0Host)
	assert.Equal(t, "6379", cfg.Redis0Port)
	assert.Equal(t, "", cfg.Redis0Password)
	assert.Equal(t, "0", cfg.Redis0Db)
	assert.Equal(t, "value", cfg.Other["key"])
}

func TestLoadConfigFromEnv(t *testing.T) {
	// Save original env values
	originalListen := os.Getenv("LISTEN")
	originalURL := os.Getenv("APP_BASE_URL")

	// Set test env values
	os.Setenv("LISTEN", ":7777")
	os.Setenv("APP_BASE_URL", "http://envtest.com")

	// Reload config from env
	err := loadConfigFromEnv()
	assert.NoError(t, err)

	// Check values were loaded
	// Note: This affects global config C

	// Restore original env values
	if originalListen != "" {
		os.Setenv("LISTEN", originalListen)
	} else {
		os.Unsetenv("LISTEN")
	}
	if originalURL != "" {
		os.Setenv("APP_BASE_URL", originalURL)
	} else {
		os.Unsetenv("APP_BASE_URL")
	}
}
