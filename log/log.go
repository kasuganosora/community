package log

import (
	"fmt"
	"github.com/sirupsen/logrus"
	"os"
	"strings"
)

var logObj *logrus.Logger

func init() {
	logObj = logrus.New()
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
