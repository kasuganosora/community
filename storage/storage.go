package storage

import (
	"bytes"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
)

type Driver interface {
	Exists(path string) bool
	Open(path string) (*bytes.Reader, error)
	Save(path string, data []byte) (savePath string, err error)
	Delete(path string) error
}

var driver Driver

func init() {
	SetDriver(config.C.StorageType)
}

func SetDriver(driverName string) {
	switch driverName {
	case "local":
		driver = &LocalStorage{}
	default:
		log.Fatal("Unknown storage driver: %s", driverName)
	}
}

func Exists(path string) bool {
	return driver.Exists(path)
}

func Open(path string) (*bytes.Reader, error) {
	return driver.Open(path)
}

func Save(path string, data []byte) (savePath string, err error) {
	return driver.Save(path, data)
}

func SaveByMultipart(path string, file *multipart.FileHeader) (savePath string, err error) {
	fp, err := file.Open()
	if err != nil {
		return
	}

	data, err := io.ReadAll(fp)
	if err != nil {
		return
	}

	// 判断有没有扩展名，没有的话加上
	ext := filepath.Ext(path)
	if ext == "" {
		ext = filepath.Ext(file.Filename)
		path += ext
	}
	return driver.Save(path, data)
}

func SaveByFilePath(path string, filePath string) (savePath string, err error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	// 判断有没有扩展名，没有的话加上
	ext := filepath.Ext(path)
	if ext == "" {
		ext = filepath.Ext(filePath)
		path += ext
	}

	return driver.Save(path, data)
}

func Delete(path string) error {
	return driver.Delete(path)
}
