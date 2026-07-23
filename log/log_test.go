package log

import (
	"bytes"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestGetLogObject(t *testing.T) {
	logger := GetLogObject()
	assert.NotNil(t, logger)
	// Should return same instance
	logger2 := GetLogObject()
	assert.Equal(t, logger, logger2)
}

func TestWriteLog(t *testing.T) {
	// Capture log output
	logger := GetLogObject()

	var buf bytes.Buffer

	logger.SetOutput(&buf)

	defer logger.SetOutput(nil)

	WriteLog(logrus.InfoLevel, "test message %s", "arg")
	assert.Contains(t, buf.String(), "test message arg")
}

func TestDebug(t *testing.T) {
	logger := GetLogObject()
	logger.SetLevel(logrus.DebugLevel)

	var buf bytes.Buffer

	logger.SetOutput(&buf)

	defer logger.SetOutput(nil)

	Debug("debug message %d", 123)
	assert.Contains(t, buf.String(), "debug message 123")
}

func TestInfo(t *testing.T) {
	logger := GetLogObject()

	var buf bytes.Buffer

	logger.SetOutput(&buf)

	defer logger.SetOutput(nil)

	Info("info message")
	assert.Contains(t, buf.String(), "info message")
}

func TestWarn(t *testing.T) {
	logger := GetLogObject()

	var buf bytes.Buffer

	logger.SetOutput(&buf)

	defer logger.SetOutput(nil)

	Warn("warn message")
	assert.Contains(t, buf.String(), "warn message")
}

func TestError(t *testing.T) {
	logger := GetLogObject()

	var buf bytes.Buffer

	logger.SetOutput(&buf)

	defer logger.SetOutput(nil)

	Error("error message")
	assert.Contains(t, buf.String(), "error message")
}

func TestErrorV(t *testing.T) {
	logger := GetLogObject()

	var buf bytes.Buffer

	logger.SetOutput(&buf)

	defer logger.SetOutput(nil)

	t.Run("with error", func(t *testing.T) {
		ErrorV(assert.AnError)
		assert.Contains(t, buf.String(), "assert.AnError")
		buf.Reset()
	})

	t.Run("with nil error", func(t *testing.T) {
		ErrorV(nil)
		assert.Empty(t, buf.String())
	})
}

func TestPanic(t *testing.T) {
	logger := GetLogObject()
	logger.SetLevel(logrus.PanicLevel)

	var buf bytes.Buffer

	logger.SetOutput(&buf)

	defer logger.SetOutput(nil)

	defer func() {
		if r := recover(); r != nil {
			assert.Contains(t, buf.String(), "panic message")
		}
	}()

	Panic("panic message")
}

func TestSetLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected logrus.Level
	}{
		{"DEBUG", logrus.DebugLevel},
		{"debug", logrus.DebugLevel},
		{"INFO", logrus.InfoLevel},
		{"info", logrus.InfoLevel},
		{"WARN", logrus.WarnLevel},
		{"warn", logrus.WarnLevel},
		{"ERROR", logrus.ErrorLevel},
		{"error", logrus.ErrorLevel},
		{"FATAL", logrus.FatalLevel},
		{"fatal", logrus.FatalLevel},
		{"PANIC", logrus.PanicLevel},
		{"panic", logrus.PanicLevel},
		{"UNKNOWN", logrus.InfoLevel}, // default
		{"", logrus.InfoLevel},        // default
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			SetLevel(tt.input)
			assert.Equal(t, tt.expected, GetLogObject().GetLevel())
		})
	}
}

func TestWriteLogWithFormat(t *testing.T) {
	logger := GetLogObject()

	var buf bytes.Buffer

	logger.SetOutput(&buf)

	defer logger.SetOutput(nil)

	WriteLog(logrus.InfoLevel, "hello %s, you have %d messages", "user", 5)
	assert.Contains(t, buf.String(), "hello user, you have 5 messages")
}

func TestLogLevelFiltering(t *testing.T) {
	logger := GetLogObject()
	logger.SetLevel(logrus.WarnLevel)

	var buf bytes.Buffer

	logger.SetOutput(&buf)

	defer logger.SetOutput(nil)

	Debug("debug should not appear")
	Info("info should not appear")
	Warn("warn should appear")
	Error("error should appear")

	output := buf.String()
	assert.NotContains(t, output, "debug should not appear")
	assert.NotContains(t, output, "info should not appear")
	assert.Contains(t, output, "warn should appear")
	assert.Contains(t, output, "error should appear")
}

func TestLogMultipleArgs(t *testing.T) {
	// Just verify Info doesn't panic with multiple args
	assert.NotPanics(t, func() {
		Info("multiple args: %s %d %f", "test", 42, 3.14)
	})
}
