package testutil

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// TestConfig holds test configuration.
type TestConfig struct {
	RedisHost     string
	RedisPort     int
	RedisPassword string
	RedisDB       int
}

// testConfig is the global test configuration.
var (
	testConfig     *TestConfig
	testConfigOnce sync.Once
)

// loadEnvFile loads environment variables from .env.test file.
func loadEnvFile(filename string) {
	file, err := os.Open(filename) //nolint:gosec // G304: reviewed and safe
	if err != nil {
		return
	}

	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Parse KEY=VALUE
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// Remove quotes if present
			if (strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"")) ||
				(strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'")) {
				value = value[1 : len(value)-1]
			}
			// Only set if not already set in environment
			if os.Getenv(key) == "" {
				_ = os.Setenv(key, value)
			}
		}
	}
}

// getEnv returns environment variable value or default.
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return defaultValue
}

// getEnvInt returns environment variable as int or default.
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if result, err := strconv.Atoi(value); err == nil {
			return result
		}
	}

	return defaultValue
}

// findProjectRoot finds the project root directory by looking for go.mod.
func findProjectRoot() string {
	// Start from current directory
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}

	for {
		// Check for go.mod
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		// Go up one directory
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root, give up
			return "."
		}

		dir = parent
	}
}

// GetTestConfig returns the test configuration
// It loads from .env.test file first, then falls back to environment variables.
func GetTestConfig() *TestConfig {
	testConfigOnce.Do(func() {
		// Find project root
		root := findProjectRoot()

		// Try to load .env.test from project root
		envFile := filepath.Join(root, ".env.test")
		loadEnvFile(envFile)

		testConfig = &TestConfig{
			RedisHost:     getEnv("REDIS_TEST_HOST", "127.0.0.1"),
			RedisPort:     getEnvInt("REDIS_TEST_PORT", 6379),
			RedisPassword: getEnv("REDIS_TEST_PASSWORD", ""),
			RedisDB:       getEnvInt("REDIS_TEST_DB", 15),
		}
	})

	return testConfig
}
