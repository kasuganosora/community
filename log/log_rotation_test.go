package log

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultLogConfig(t *testing.T) {
	config := DefaultLogConfig()

	if config.MaxSize != 100 {
		t.Errorf("expected MaxSize=100, got %d", config.MaxSize)
	}
	if config.MaxBackups != 10 {
		t.Errorf("expected MaxBackups=10, got %d", config.MaxBackups)
	}
	if config.MaxAge != 30 {
		t.Errorf("expected MaxAge=30, got %d", config.MaxAge)
	}
	if !config.Compress {
		t.Error("expected Compress=true")
	}
	if config.MaxDiskUsage != 1024 {
		t.Errorf("expected MaxDiskUsage=1024, got %d", config.MaxDiskUsage)
	}
}

func TestGetDirSize(t *testing.T) {
	// 创建临时目录
	tempDir, err := os.MkdirTemp("", "log_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// 创建测试文件
	testFile := filepath.Join(tempDir, "test.log")
	content := []byte("test content")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	// 测试获取目录大小
	size := getDirSize(tempDir)
	if size != int64(len(content)) {
		t.Errorf("expected size=%d, got %d", len(content), size)
	}
}

func TestGetLogStats(t *testing.T) {
	// 创建临时目录
	tempDir, err := os.MkdirTemp("", "log_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	config := LogConfig{
		FilePath:     filepath.Join(tempDir, "test.log"),
		MaxSize:      100,
		MaxBackups:   10,
		MaxAge:       30,
		Compress:     true,
		MaxDiskUsage: 100,
	}

	// 创建测试日志文件
	testLogFile := filepath.Join(tempDir, "test.log")
	if err := os.WriteFile(testLogFile, []byte("test log content"), 0644); err != nil {
		t.Fatalf("failed to write test log: %v", err)
	}

	// 测试获取统计信息
	stats := GetLogStats(config)

	// 验证配置信息
	configInfo, ok := stats["config"].(map[string]interface{})
	if !ok {
		t.Fatal("expected config in stats")
	}
	if configInfo["max_size_mb"] != 100 {
		t.Errorf("expected max_size_mb=100, got %v", configInfo["max_size_mb"])
	}

	// 验证文件统计
	if stats["log_file_count"] != 1 {
		t.Errorf("expected log_file_count=1, got %v", stats["log_file_count"])
	}
}

func TestCleanupOldLogs(t *testing.T) {
	// 创建临时目录
	tempDir, err := os.MkdirTemp("", "log_cleanup_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	config := LogConfig{
		MaxDiskUsage: 1, // 1MB 限制
	}

	// 创建测试日志文件（模拟超过限制）
	// 创建 10 个 150KB 的文件 = 1.5MB，超过 1MB 限制
	for i := 0; i < 10; i++ {
		filename := filepath.Join(tempDir, fmt.Sprintf("test.log.%d", i))
		// 创建 150KB 的文件
		content := make([]byte, 150*1024)
		if err := os.WriteFile(filename, content, 0644); err != nil {
			t.Fatalf("failed to write test file: %v", err)
		}
		// 修改文件时间使其不同（越旧的索引越大）
		oldTime := time.Now().Add(-time.Duration(10-i) * time.Hour)
		_ = os.Chtimes(filename, oldTime, oldTime)
	}

	// 执行清理
	cleanupOldLogs(tempDir, config)

	// 验证文件是否被清理
	files, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}

	// 应该保留部分文件（清理后低于 80% 限制）
	// 1MB * 0.8 = 819.2KB，每个文件 150KB，最多保留 5 个
	// 原始 10 个文件，应该清理掉一部分
	if len(files) >= 10 {
		t.Errorf("expected some files to be cleaned up, got %d files", len(files))
	}

	t.Logf("Files after cleanup: %d", len(files))
}

func TestSetGlobalLogConfig(t *testing.T) {
	config := LogConfig{
		FilePath:   "/tmp/test.log",
		MaxSize:    50,
		MaxBackups: 5,
	}

	SetGlobalLogConfig(config)

	if globalLogConfig.FilePath != "/tmp/test.log" {
		t.Errorf("expected FilePath=/tmp/test.log, got %s", globalLogConfig.FilePath)
	}
}
