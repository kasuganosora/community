package utils

import (
	"github.com/pkg/errors"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// sourceDir 源文件夹
var sourceDir string

func init() {
	_, file, _, _ := runtime.Caller(0)
	sourceDir = regexp.MustCompile(`utils.file\.go`).ReplaceAllString(file, "")
}

// ReadFileToString 以字符串形式读取文件
func ReadFileToString(filename string) (content string, err error) {
	ret, err := os.ReadFile(filename)
	if err != nil {
		err = errors.WithMessagef(err, "Read file %s have error: %v", filename, err)
		return
	}

	content = string(ret)

	return
}

// FileWithLineNum 返回调用者当前行
func FileWithLineNum(level ...int) string {
	start := 2

	if len(level) > 0 {
		start = level[0]
	}

	for i := start; i < 15; i++ {
		_, file, line, ok := runtime.Caller(i)

		if !ok {
			continue
		}

		if strings.HasPrefix(file, sourceDir) || strings.HasSuffix(file, "_test.go") {
			return file + ":" + strconv.FormatInt(int64(line), 10)
		}
	}

	return ""
}

// IsFileExists 判断文件是否存在
func IsFileExists(path string) bool {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}

	return true
}

// GetFullPath 获取文件的绝对路径
func GetFullPath(path string) string {
	if strings.HasPrefix(path, "/") {
		return path
	} else if runtime.GOOS == "windows" {
		// 判断开始有没有盘符
		if len(path) > 1 && strings.Contains(path[0:3], ":") {
			return path
		}
	}

	return sourceDir + path
}
