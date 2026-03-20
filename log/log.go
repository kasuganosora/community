package log

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"gopkg.in/natefinch/lumberjack.v2"
)

var logObj *logrus.Logger

// LogConfig 日志配置
type LogConfig struct {
	Level      string // debug, info, warn, error
	Output     string // stdout, file, both
	FilePath   string
	MaxSize    int  // MB
	MaxBackups int  // 保留旧文件个数
	MaxAge     int  // 保留天数
	Compress   bool // 是否压缩
	Format     string // text, json
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

// InitLogger 初始化日志配置
func InitLogger(config LogConfig) {
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

	// 设置输出
	switch config.Output {
	case "file":
		logObj.SetOutput(getLogWriter(config))
	case "both":
		logWriter := getLogWriter(config)
		logObj.SetOutput(io.MultiWriter(os.Stdout, logWriter))
	default: // stdout
		logObj.SetOutput(os.Stdout)
	}
}

// getLogWriter 获取日志写入器（支持轮转）
func getLogWriter(config LogConfig) io.Writer {
	// 确保日志目录存在
	if config.FilePath != "" {
		dir := filepath.Dir(config.FilePath)
		if dir != "" && dir != "." {
			os.MkdirAll(dir, 0755)
		}
	}

	return &lumberjack.Logger{
		Filename:   config.FilePath,
		MaxSize:    config.MaxSize,    // MB
		MaxBackups: config.MaxBackups, // 保留旧文件个数
		MaxAge:     config.MaxAge,     // 保留天数
		Compress:   config.Compress,   // 是否压缩
	}
}

func GetLogObject() *logrus.Logger {
	return logObj
}

// WriteLog 写入日志
func WriteLog(level logrus.Level, msg string, args ...any) {
	msg = fmt.Sprintf(msg, args...)
	logObj.Log(level, msg)
}

// Debug 调试日志
func Debug(msg string, args ...any) {
	WriteLog(logrus.DebugLevel, msg, args...)
}

// Info 信息日志
func Info(msg string, args ...any) {
	WriteLog(logrus.InfoLevel, msg, args...)
}

// Warn 警告日志
func Warn(msg string, args ...any) {
	WriteLog(logrus.WarnLevel, msg, args...)
}

// Error 错误日志
func Error(msg string, args ...any) {
	WriteLog(logrus.ErrorLevel, msg, args...)
}

// Fatal 致命错误日志
func Fatal(msg string, args ...any) {
	WriteLog(logrus.FatalLevel, msg, args...)
	os.Exit(1)
}

// FatalIf 致命错误日志, 如果错误不为空
func FatalIf(err error) {
	if err == nil {
		return
	}

	msg := fmt.Sprintf("%+v", err)
	Fatal(msg)
}

// Panic 恐慌日志
func Panic(msg string, args ...any) {
	WriteLog(logrus.PanicLevel, msg, args...)
}

// ErrorV 错误日志
func ErrorV(err error) {
	if err == nil {
		return
	}

	msg := fmt.Sprintf("%+v", err)
	Error(msg)
}

// WithFields 创建带字段的日志条目
func WithFields(fields map[string]interface{}) *logrus.Entry {
	return logObj.WithFields(fields)
}

// WithField 创建带单个字段的日志条目
func WithField(key string, value interface{}) *logrus.Entry {
	return logObj.WithField(key, value)
}

// WithError 创建带错误字段的日志条目
func WithError(err error) *logrus.Entry {
	return logObj.WithError(err)
}

// SetLevel 设置日志等级
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
