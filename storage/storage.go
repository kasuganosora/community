package storage

import (
	"bytes"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
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

func Delete(path string) error {
	return driver.Delete(path)
}
