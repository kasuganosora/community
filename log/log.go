package log

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	logObj *logrus.Logger
	// fileWriter 追踪当前 lumberjack 写入器（实现 io.Closer），
	// 以便 CloseLog() 能释放文件句柄，以及 InitLogger() 多次调用时关闭旧的写入器。
	fileWriter   io.Closer
	fileWriterMu sync.Mutex
)

// LogConfig 日志配置.
type LogConfig struct {
	Level        string // debug, info, warn, error
	Output       string // stdout, file, both
	FilePath     string
	MaxSize      int    // MB，单个日志文件最大大小
	MaxBackups   int    // 保留旧文件个数
	MaxAge       int    // 保留天数
	Compress     bool   // 是否压缩
	Format       string // text, json
	MaxDiskUsage int64  // MB，日志目录最大磁盘使用量（0表示不限制）
}

func init() {
	logObj = logrus.New()
	// 默认配置
	logObj.SetOutput(os.Stdout)
	logObj.SetLevel(logrus.InfoLevel)
	logObj.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339,
	})
}

// DefaultLogConfig 返回安全的默认日志配置
// 默认配置确保日志不会占满磁盘：
// - 单个文件最大 100MB
// - 最多保留 10 个备份文件
// - 最多保留 30 天
// - 启用压缩节省空间
// - 日志目录最大 1GB.
func DefaultLogConfig() LogConfig {
	return LogConfig{
		Level:        "info",
		Output:       "stdout",
		FilePath:     "logs/community.log",
		MaxSize:      100,  // 单个文件最大 100MB
		MaxBackups:   10,   // 保留 10 个备份
		MaxAge:       30,   // 保留 30 天
		Compress:     true, // 启用压缩
		Format:       "text",
		MaxDiskUsage: 1024, // 日志目录最大 1GB
	}
}

// InitLogger 初始化日志配置.
func InitLogger(config LogConfig) {
	// 保存配置用于后续统计
	SetGlobalLogConfig(config)

	// 设置默认值
	if config.MaxSize <= 0 {
		config.MaxSize = 100
	}

	if config.MaxBackups <= 0 {
		config.MaxBackups = 10
	}

	if config.MaxAge <= 0 {
		config.MaxAge = 30
	}

	if config.MaxDiskUsage <= 0 {
		config.MaxDiskUsage = 1024 // 默认 1GB
	}

	// 设置日志级别
	level, err := logrus.ParseLevel(config.Level)
	if err != nil {
		level = logrus.InfoLevel
	}

	logObj.SetLevel(level)

	// 设置日志格式
	if config.Format == "json" {
		logObj.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: time.RFC3339,
		})
	} else {
		logObj.SetFormatter(&logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: time.RFC3339,
		})
	}

	// 设置输出。
	// 先关闭上一次 InitLogger 可能创建的 lumberjack 写入器，避免文件句柄泄漏。
	closeFileWriter()

	switch config.Output {
	case "file":
		w := getLogWriter(config)
		trackFileWriter(w)
		logObj.SetOutput(w)
	case "both":
		w := getLogWriter(config)
		trackFileWriter(w)
		logObj.SetOutput(io.MultiWriter(os.Stdout, w))
	default: // stdout
		logObj.SetOutput(os.Stdout)
	}
}

// trackFileWriter 记录当前的文件写入器（若它实现了 io.Closer），
// 供 CloseLog() 或后续 InitLogger() 调用释放资源。
func trackFileWriter(w io.Writer) {
	fileWriterMu.Lock()
	defer fileWriterMu.Unlock()
	if closer, ok := w.(io.Closer); ok {
		fileWriter = closer
	} else {
		fileWriter = nil
	}
}

// closeFileWriter 关闭并清空当前追踪的文件写入器。
func closeFileWriter() {
	fileWriterMu.Lock()
	defer fileWriterMu.Unlock()
	if fileWriter != nil {
		_ = fileWriter.Close()
		fileWriter = nil
	}
}

// CloseLog 释放日志资源（关闭文件写入器）。
// 应在程序优雅关闭时调用（例如主函数的 defer）。
func CloseLog() {
	closeFileWriter()
}

// getLogWriter 获取日志写入器（支持轮转）.
func getLogWriter(config LogConfig) io.Writer {
	// 确保日志目录存在
	if config.FilePath != "" {
		dir := filepath.Dir(config.FilePath)
		if dir != "" && dir != "." {
			err := os.MkdirAll(dir, 0755) //nolint:gosec // G301: reviewed and safe
			if err != nil {
				logObj.Warnf("failed to create log directory %s: %v", dir, err)
			}
		}

		// 检查磁盘空间
		if config.MaxDiskUsage > 0 {
			if usage := getDirSize(dir); usage > config.MaxDiskUsage*1024*1024 {
				// 磁盘使用超过限制，清理旧日志
				cleanupOldLogs(dir, config)
			}
		}
	}

	// 使用 lumberjack 实现日志轮转
	// 配置说明：
	// - MaxSize: 单个日志文件最大大小（MB），超过后自动轮转
	// - MaxBackups: 保留的旧日志文件个数，超过后删除最旧的
	// - MaxAge: 日志文件保留天数，超过后自动删除
	// - Compress: 是否压缩旧日志（gzip），节省磁盘空间
	return &lumberjack.Logger{
		Filename:   config.FilePath,
		MaxSize:    config.MaxSize,    // MB，单个文件最大大小
		MaxBackups: config.MaxBackups, // 保留旧文件个数
		MaxAge:     config.MaxAge,     // 保留天数
		Compress:   config.Compress,   // 是否压缩（推荐启用）
		LocalTime:  true,              // 使用本地时间
	}
}

// getDirSize 获取目录大小（字节）.
func getDirSize(dir string) int64 {
	var size int64

	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			size += info.Size()
		}

		return nil
	})

	return size
}

// cleanupOldLogs 清理旧日志文件.
func cleanupOldLogs(dir string, config LogConfig) {
	// 获取所有日志文件
	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	// 按修改时间排序
	type fileInfo struct {
		path    string
		modTime time.Time
		size    int64
	}

	var logFiles []fileInfo

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		info, err := file.Info()
		if err != nil {
			continue
		}
		// 只处理日志文件（包括轮转后的日志如 test.log.1, test.log.2.gz 等）
		name := file.Name()
		if strings.Contains(name, ".log") || strings.HasSuffix(name, ".gz") {
			logFiles = append(logFiles, fileInfo{
				path:    filepath.Join(dir, name),
				modTime: info.ModTime(),
				size:    info.Size(),
			})
		}
	}

	// 按修改时间排序（最旧的在前）
	for i := range len(logFiles) - 1 {
		for j := i + 1; j < len(logFiles); j++ {
			if logFiles[i].modTime.After(logFiles[j].modTime) {
				logFiles[i], logFiles[j] = logFiles[j], logFiles[i]
			}
		}
	}

	// 删除旧日志直到低于限制
	var currentSize int64
	for _, f := range logFiles {
		currentSize += f.size
	}

	targetSize := config.MaxDiskUsage * 1024 * 1024 * 80 / 100 // 目标使用 80%
	for _, f := range logFiles {
		if currentSize <= targetSize {
			break
		}
		// 删除最旧的日志
		err := os.Remove(f.path)
		if err == nil {
			currentSize -= f.size
			logObj.Warn("Cleaned up old log file: " + f.path)
		}
	}
}

// GetLogStats 获取日志统计信息.
func GetLogStats(config LogConfig) map[string]any {
	stats := map[string]any{
		"config": map[string]any{
			"max_size_mb":       config.MaxSize,
			"max_backups":       config.MaxBackups,
			"max_age_days":      config.MaxAge,
			"compress":          config.Compress,
			"max_disk_usage_mb": config.MaxDiskUsage,
		},
	}

	if config.FilePath != "" {
		dir := filepath.Dir(config.FilePath)
		if size := getDirSize(dir); size > 0 {
			stats["current_size_mb"] = size / 1024 / 1024
			stats["current_size_bytes"] = size
		}

		// 统计日志文件数量
		files, _ := os.ReadDir(dir)

		var logFileCount int

		for _, f := range files {
			if !f.IsDir() && (strings.HasSuffix(f.Name(), ".log") || strings.HasSuffix(f.Name(), ".gz")) {
				logFileCount++
			}
		}

		stats["log_file_count"] = logFileCount

		// 检查是否接近磁盘限制
		if config.MaxDiskUsage > 0 {
			currentSize := getDirSize(dir)
			usagePercent := float64(currentSize) / float64(config.MaxDiskUsage*1024*1024) * 100
			stats["disk_usage_percent"] = usagePercent
			stats["is_near_limit"] = usagePercent > 80
		}
	}

	return stats
}

// globalLogConfig 全局日志配置（用于统计），通过 globalConfigMu 保护并发访问.
var (
	globalLogConfig LogConfig
	globalConfigMu  sync.RWMutex
)

// SetGlobalLogConfig 设置全局日志配置.
func SetGlobalLogConfig(config LogConfig) {
	globalConfigMu.Lock()
	globalLogConfig = config
	globalConfigMu.Unlock()
}

// GetCurrentLogStats 获取当前日志统计（使用全局配置）.
func GetCurrentLogStats() map[string]any {
	globalConfigMu.RLock()
	cfg := globalLogConfig
	globalConfigMu.RUnlock()

	// 读取路径不应有写副作用；用局部变量做默认值填充
	if cfg.FilePath == "" {
		cfg = DefaultLogConfig()
	}

	return GetLogStats(cfg)
}

func GetLogObject() *logrus.Logger {
	return logObj
}

// WriteLog 写入日志.
func WriteLog(level logrus.Level, msg string, args ...any) {
	// 仅在有格式化参数时才调用 Sprintf，避免消息本身含 % 字符（如 URL、
	// 百分比、用户输入）被误解析为格式化动词而输出 "%!x(MISSING)"。
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	logObj.Log(level, msg)
}

// Debug 调试日志.
func Debug(msg string, args ...any) {
	WriteLog(logrus.DebugLevel, msg, args...)
}

// Info 信息日志.
func Info(msg string, args ...any) {
	WriteLog(logrus.InfoLevel, msg, args...)
}

// Warn 警告日志.
func Warn(msg string, args ...any) {
	WriteLog(logrus.WarnLevel, msg, args...)
}

// Error 错误日志.
func Error(msg string, args ...any) {
	WriteLog(logrus.ErrorLevel, msg, args...)
}

// Fatal 致命错误日志.
func Fatal(msg string, args ...any) {
	WriteLog(logrus.FatalLevel, msg, args...)
	os.Exit(1)
}

// FatalIf 致命错误日志, 如果错误不为空.
func FatalIf(err error) {
	if err == nil {
		return
	}

	msg := fmt.Sprintf("%+v", err)
	Fatal(msg)
}

// Panic 恐慌日志.
func Panic(msg string, args ...any) {
	WriteLog(logrus.PanicLevel, msg, args...)
}

// ErrorV 错误日志.
func ErrorV(err error) {
	if err == nil {
		return
	}

	msg := fmt.Sprintf("%+v", err)
	Error(msg)
}

// WithFields 创建带字段的日志条目.
func WithFields(fields map[string]any) *logrus.Entry {
	return logObj.WithFields(fields)
}

// WithField 创建带单个字段的日志条目.
func WithField(key string, value any) *logrus.Entry {
	return logObj.WithField(key, value)
}

// WithError 创建带错误字段的日志条目.
func WithError(err error) *logrus.Entry {
	return logObj.WithError(err)
}

// SetLevel 设置日志等级.
func SetLevel(level string) {
	level = strings.ToUpper(level)

	var lv logrus.Level

	switch level {
	case "DEBUG":
		lv = logrus.DebugLevel
	case "INFO":
		lv = logrus.InfoLevel
	case "WARN":
		lv = logrus.WarnLevel
	case "ERROR":
		lv = logrus.ErrorLevel
	case "FATAL":
		lv = logrus.FatalLevel
	case "PANIC":
		lv = logrus.PanicLevel
	default:
		lv = logrus.InfoLevel
	}

	logObj.SetLevel(lv)
}
