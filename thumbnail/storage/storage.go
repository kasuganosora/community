package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Backend 存储后端接口
type Backend interface {
	// Type 存储类型
	Type() string

	// Put 上传文件
	Put(ctx context.Context, key string, data io.Reader, size int64, contentType string) error

	// GetURL 获取访问URL
	GetURL(key string) string

	// Exists 检查是否存在
	Exists(ctx context.Context, key string) (bool, error)

	// Delete 删除
	Delete(ctx context.Context, key string) error
}

// StorageType 常量
const (
	StorageTypeLocal = "local"
	StorageTypeS3    = "s3"
	StorageTypeMinIO = "minio"
)

// NewBackend 创建存储后端
func NewBackend(cfg Config) (Backend, error) {
	switch cfg.Type {
	case StorageTypeLocal:
		return NewLocalBackend(cfg)
	case StorageTypeS3:
		return NewS3Backend(cfg)
	case StorageTypeMinIO:
		return NewMinIOBackend(cfg)
	default:
		return nil, fmt.Errorf("unsupported storage type: %s", cfg.Type)
	}
}

// LocalBackend 本地存储后端
type LocalBackend struct {
	BasePath   string
	PathPrefix string
	PublicURL  string
}

// NewLocalBackend 创建本地存储后端
func NewLocalBackend(cfg Config) (*LocalBackend, error) {
	basePath := cfg.LocalPath
	if basePath == "" {
		basePath = "./storage/thumbnails"
	}

	// 确保目录存在
	if err := os.MkdirAll(basePath, 0755); err != nil {
		return nil, fmt.Errorf("failed to create storage directory: %w", err)
	}

	return &LocalBackend{
		BasePath:   basePath,
		PathPrefix: cfg.PathPrefix,
		PublicURL:  cfg.PublicURL,
	}, nil
}

// Type 存储类型
func (l *LocalBackend) Type() string {
	return "local"
}

// Put 上传文件
func (l *LocalBackend) Put(ctx context.Context, key string, data io.Reader, size int64, contentType string) error {
	path := filepath.Join(l.BasePath, l.PathPrefix, key)

	// 确保目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// 创建文件
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	// 写入数据
	_, err = io.Copy(file, data)
	return err
}

// GetURL 获取访问URL
func (l *LocalBackend) GetURL(key string) string {
	if l.PublicURL != "" {
		return fmt.Sprintf("%s/%s%s", l.PublicURL, l.PathPrefix, key)
	}
	return fmt.Sprintf("/thumbnails/%s%s", l.PathPrefix, key)
}

// Exists 检查是否存在
func (l *LocalBackend) Exists(ctx context.Context, key string) (bool, error) {
	path := filepath.Join(l.BasePath, l.PathPrefix, key)
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// Delete 删除
func (l *LocalBackend) Delete(ctx context.Context, key string) error {
	path := filepath.Join(l.BasePath, l.PathPrefix, key)
	return os.Remove(path)
}
