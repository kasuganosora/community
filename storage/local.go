package storage

import (
	"bytes"
	"github.com/kasuganosora/community/utils"
	"os"
	"path/filepath"
	"strings"
)

type LocalStorage struct {
	Root string
}

// hasPrefixPath 验证 path 是否在 root 目录内.
// 使用 root + 分隔符前缀检查，避免 "uploadsfoo" 被误判为 "uploads" 的子路径.
func hasPrefixPath(path, root string) bool {
	if path == root {
		return true
	}

	return strings.HasPrefix(path, root+string(filepath.Separator))
}

func (l *LocalStorage) Exists(path string) bool {
	path = filepath.Clean(filepath.Join(l.Root, path))
	// 判断路径是否合法
	if !hasPrefixPath(path, l.Root) {
		return false
	}

	return utils.IsFileExists(path)
}

func (l *LocalStorage) Open(path string) (reader *bytes.Reader, err error) {
	if !l.Exists(path) {
		err = os.ErrNotExist

		return
	}

	path = filepath.Clean(filepath.Join(l.Root, path))

	fileData, err := os.ReadFile(path)
	if err != nil {
		return
	}

	reader = bytes.NewReader(fileData)

	return
}

func (l *LocalStorage) Save(path string, data []byte) (savePath string, err error) {
	path = filepath.Clean(filepath.Join(l.Root, path))
	// 判断路径是否合法
	if !hasPrefixPath(path, l.Root) {
		err = os.ErrInvalid

		return savePath, err
	}

	// 判断文件夹是否存在
	dir := filepath.Dir(path)
	if !utils.IsFileExists(dir) {
		err = os.MkdirAll(dir, os.ModePerm) //nolint:gosec // G301: reviewed and safe
		if err != nil {
			return savePath, err
		}
	}

	// 创建/覆盖文件
	var file *os.File
	if !utils.IsFileExists(path) {
		file, err = os.Create(path)
		if err != nil {
			return savePath, err
		}
	} else {
		file, err = os.OpenFile(path, os.O_WRONLY, os.ModePerm) //nolint:gosec // G302: reviewed and safe
		if err != nil {
			return savePath, err
		}
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	_, err = file.Write(data)

	savePath = path[len(l.Root):]

	return savePath, err
}

func (l *LocalStorage) Delete(path string) (err error) {
	path = filepath.Clean(filepath.Join(l.Root, path))
	// 判断路径是否合法
	if !hasPrefixPath(path, l.Root) {
		return os.ErrInvalid
	}

	if !utils.IsFileExists(path) {
		return os.ErrNotExist
	}

	err = os.RemoveAll(path)

	return
}
