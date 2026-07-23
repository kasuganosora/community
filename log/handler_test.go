package log

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// Set gin to test mode
	gin.SetMode(gin.TestMode)
}

// setupTestRouter creates a test gin router with the given handler.
func setupTestRouter(handler gin.HandlerFunc) *gin.Engine {
	router := gin.New()
	router.GET("/test", handler)

	return router
}

// setupTempLogDir creates a temporary directory with a log file and returns its path.
// The caller is responsible for cleaning up via the returned cleanup function.
func setupTempLogDir(t *testing.T, fileSize int64) (string, func()) {
	t.Helper()

	tempDir := t.TempDir()

	logFile := filepath.Join(tempDir, "test.log")
	// Write some content to create a file of the specified size
	content := make([]byte, fileSize)
	for i := range content {
		content[i] = 'a'
	}

	err := os.WriteFile(logFile, content, 0644) //nolint:gosec // G306: reviewed and safe
	require.NoError(t, err)

	return tempDir, func() {}
}

// --- StatsHandler tests ---

func TestStatsHandler_ReturnsOK(t *testing.T) {
	// Set a known global config
	SetGlobalLogConfig(DefaultLogConfig())

	router := setupTestRouter(StatsHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx // test code
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestStatsHandler_ContainsConfigKeys(t *testing.T) {
	SetGlobalLogConfig(DefaultLogConfig())

	router := setupTestRouter(StatsHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	// The response should contain the "config" key from GetLogStats
	config, ok := body["config"].(map[string]any)
	assert.True(t, ok, "response should contain a 'config' object")
	assert.NotNil(t, config)

	// Verify expected config fields
	assert.Contains(t, config, "max_size_mb")
	assert.Contains(t, config, "max_backups")
	assert.Contains(t, config, "max_age_days")
	assert.Contains(t, config, "compress")
	assert.Contains(t, config, "max_disk_usage_mb")
}

func TestStatsHandler_DefaultConfigValues(t *testing.T) {
	config := DefaultLogConfig()
	SetGlobalLogConfig(config)

	router := setupTestRouter(StatsHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	configData, ok := body["config"].(map[string]any)
	assert.True(t, ok)
	assert.InDelta(t, float64(100), configData["max_size_mb"], 0)
	assert.InDelta(t, float64(10), configData["max_backups"], 0)
	assert.InDelta(t, float64(30), configData["max_age_days"], 0)
	assert.Equal(t, true, configData["compress"])
	assert.InDelta(t, float64(1024), configData["max_disk_usage_mb"], 0)
}

func TestStatsHandler_WithEmptyFilePath(t *testing.T) {
	// When FilePath is empty, GetCurrentLogStats will use DefaultLogConfig
	SetGlobalLogConfig(LogConfig{
		FilePath:   "",
		MaxSize:    200,
		MaxBackups: 5,
		MaxAge:     7,
		Compress:   false,
	})

	router := setupTestRouter(StatsHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.NotNil(t, body)
}

func TestStatsHandler_WithTempLogFile(t *testing.T) {
	tempDir, cleanup := setupTempLogDir(t, 1024) // 1KB file
	defer cleanup()

	SetGlobalLogConfig(LogConfig{
		FilePath:     filepath.Join(tempDir, "test.log"),
		MaxSize:      100,
		MaxBackups:   10,
		MaxAge:       30,
		Compress:     true,
		MaxDiskUsage: 1024,
	})

	router := setupTestRouter(StatsHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	// Should have file count
	assert.InDelta(t, float64(1), body["log_file_count"], 0)
}

func TestStatsHandler_InvalidMethod(t *testing.T) {
	SetGlobalLogConfig(DefaultLogConfig())

	router := gin.New()
	router.GET("/stats", StatsHandler())

	// POST to a GET route should return 404 (gin default for method not allowed)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/stats", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	// Gin returns 404 for method not matching (since POST is not registered)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// --- HealthHandler tests ---

func TestHealthHandler_ReturnsOK(t *testing.T) {
	SetGlobalLogConfig(DefaultLogConfig())

	router := setupTestRouter(HealthHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHealthHandler_HealthyWhenNotNearLimit(t *testing.T) {
	tempDir, cleanup := setupTempLogDir(t, 100) // small file, well under limit
	defer cleanup()

	SetGlobalLogConfig(LogConfig{
		FilePath:     filepath.Join(tempDir, "test.log"),
		MaxSize:      100,
		MaxBackups:   10,
		MaxAge:       30,
		Compress:     true,
		MaxDiskUsage: 1024, // 1GB limit, tiny file
	})

	router := setupTestRouter(HealthHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	assert.Equal(t, "healthy", body["status"])
	assert.Equal(t, "Log system is healthy", body["message"])
	assert.NotNil(t, body["stats"])
}

func TestHealthHandler_WarningWhenNearLimit(t *testing.T) {
	// Create a file that exceeds 80% of MaxDiskUsage
	// = 1KB (1 MB in config = 1024*1024 bytes, but we use raw values)
	// To trigger > 80%, we need actual file size > 0.8 * MaxDiskUsage * 1024 * 1024
	// Let's use MaxDiskUsage=1 (1MB) and create a file that is > 0.8MB = 838860 bytes
	fileSize := int64(900 * 1024) // 900KB > 80% of 1MB

	tempDir, cleanup := setupTempLogDir(t, fileSize)
	defer cleanup()

	SetGlobalLogConfig(LogConfig{
		FilePath:     filepath.Join(tempDir, "test.log"),
		MaxSize:      100,
		MaxBackups:   10,
		MaxAge:       30,
		Compress:     true,
		MaxDiskUsage: 1, // 1MB limit, file is 900KB (>80%)
	})

	router := setupTestRouter(HealthHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	assert.Equal(t, "warning", body["status"])
	assert.Equal(t, "Log disk usage is above 80%, cleanup recommended", body["message"])
	assert.NotNil(t, body["stats"])
}

func TestHealthHandler_HasStatsField(t *testing.T) {
	SetGlobalLogConfig(DefaultLogConfig())

	router := setupTestRouter(HealthHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	// The "stats" field should contain the log stats
	stats, ok := body["stats"].(map[string]any)
	assert.True(t, ok, "response should contain a 'stats' object")
	assert.NotNil(t, stats)
	assert.Contains(t, stats, "config")
}

func TestHealthHandler_DefaultConfigIsHealthy(t *testing.T) {
	// With default config, no files on disk means not near limit
	SetGlobalLogConfig(LogConfig{
		FilePath:     "",
		MaxSize:      100,
		MaxBackups:   10,
		MaxAge:       30,
		Compress:     true,
		MaxDiskUsage: 1024,
	})

	router := setupTestRouter(HealthHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "healthy", body["status"])
}

func TestHealthHandler_ResponseHasRequiredKeys(t *testing.T) {
	SetGlobalLogConfig(DefaultLogConfig())

	router := setupTestRouter(HealthHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	// Verify all required keys exist
	_, hasStatus := body["status"]
	_, hasMessage := body["message"]
	_, hasStats := body["stats"]

	assert.True(t, hasStatus, "response should contain 'status' key")
	assert.True(t, hasMessage, "response should contain 'message' key")
	assert.True(t, hasStats, "response should contain 'stats' key")
}

func TestHealthHandler_WarningWithMultipleLogFiles(t *testing.T) {
	// Create multiple log files to simulate disk pressure
	tempDir := t.TempDir()

	// Create 3 log files, each 300KB, total 900KB > 80% of 1MB limit
	for i := range 3 {
		filename := filepath.Join(tempDir, "app.log")
		if i > 0 {
			filename = filepath.Join(tempDir, "app.log."+string(rune('0'+i)))
		}

		content := make([]byte, 300*1024)
		for j := range content {
			content[j] = 'b'
		}

		err := os.WriteFile(filename, content, 0644) //nolint:gosec // G306: reviewed and safe
		require.NoError(t, err)
	}

	SetGlobalLogConfig(LogConfig{
		FilePath:     filepath.Join(tempDir, "app.log"),
		MaxSize:      100,
		MaxBackups:   10,
		MaxAge:       30,
		Compress:     true,
		MaxDiskUsage: 1, // 1MB limit
	})

	router := setupTestRouter(HealthHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)
	assert.Equal(t, "warning", body["status"])
}

func TestHealthHandler_EdgeCaseExactly80Percent(t *testing.T) {
	// Create a file exactly at 80% of MaxDiskUsage
	// MaxDiskUsage=1MB = 1048576 bytes, 80% = 838860.8 bytes
	// We'll use 800KB which is under 80% to ensure "healthy"
	fileSize := int64(800 * 1024) // 800KB < 838860 bytes (80% of 1MB)

	tempDir, cleanup := setupTempLogDir(t, fileSize)
	defer cleanup()

	SetGlobalLogConfig(LogConfig{
		FilePath:     filepath.Join(tempDir, "test.log"),
		MaxSize:      100,
		MaxBackups:   10,
		MaxAge:       30,
		Compress:     true,
		MaxDiskUsage: 1,
	})

	router := setupTestRouter(HealthHandler())
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/test", nil) //nolint:noctx
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var body map[string]any

	err := json.Unmarshal(w.Body.Bytes(), &body)
	require.NoError(t, err)

	// Should still be healthy since 800KB / 1MB = 76.29% < 80%
	assert.Equal(t, "healthy", body["status"])
}
