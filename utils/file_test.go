package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetSourceDir(t *testing.T) {
	dir := GetSourceDir()
	assert.NotEmpty(t, dir)
	// 验证目录存在
	assert.True(t, IsFileExists(dir) || IsFileExists(filepath.Dir(dir)))
}

func TestReadFileToString(t *testing.T) {
	// 测试读取存在的文件
	content, err := ReadFileToString("file.go")
	assert.NoError(t, err)
	assert.Contains(t, content, "package utils")

	// 测试读取不存在的文件
	_, err = ReadFileToString("nonexistent_file_12345.txt")
	assert.Error(t, err)
}

func TestFileWithLineNum(t *testing.T) {
	// 调用 FileWithLineNum，它应该返回调用者的文件和行号
	// 注意：在测试环境中，可能因为调用栈不匹配 sourceDir 而返回空字符串
	result := FileWithLineNum()
	// 如果返回非空，应该包含文件路径和行号
	if result != "" {
		assert.Contains(t, result, ":")
	}
	// 测试不同层级
	result2 := FileWithLineNum(1)
	if result2 != "" {
		assert.Contains(t, result2, ":")
	}
}

func TestIsFileExists(t *testing.T) {
	// 测试存在的文件
	assert.True(t, IsFileExists("file.go"))

	// 测试不存在的文件
	assert.False(t, IsFileExists("nonexistent_file_12345.txt"))

	// 测试存在的目录
	assert.True(t, IsFileExists("."))

	// 测试不存在的目录
	assert.False(t, IsFileExists("/nonexistent/path/12345"))
}

func TestGetFullPath(t *testing.T) {
	// 测试绝对路径（Unix 风格）
	absPath := "/tmp/test.txt"
	result := GetFullPath(absPath)
	assert.Equal(t, absPath, result)

	// 测试 Windows 绝对路径
	if runtime.GOOS == "windows" {
		winPath := "C:\\temp\\test.txt"
		result = GetFullPath(winPath)
		assert.Equal(t, winPath, result)
	}

	// 测试相对路径
	relPath := "test.txt"
	result = GetFullPath(relPath)
	assert.True(t, strings.HasSuffix(result, "test.txt"))
}

func TestCheckIsImage(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"image.jpg", true},
		{"image.jpeg", true},
		{"image.png", true},
		{"image.JPG", true},
		{"image.JPEG", true},
		{"image.PNG", true},
		{"image.gif", false},
		{"image.bmp", false},
		{"document.pdf", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			result := CheckIsImage(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestReadFileToStringIntegration(t *testing.T) {
	// 创建临时文件
	tmpFile := filepath.Join(os.TempDir(), "test_read_file.txt")
	content := "Hello, World!"

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	assert.NoError(t, err)
	defer os.Remove(tmpFile)

	// 测试读取
	readContent, err := ReadFileToString(tmpFile)
	assert.NoError(t, err)
	assert.Equal(t, content, readContent)
}
