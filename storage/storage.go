package storage

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// StorageProvider 存储提供者接口
type StorageProvider interface {
	Upload(filePath string, objectKey string) (string, error)
	UploadReader(reader io.Reader, objectKey string, size int64) (string, error)
	Download(objectKey string) (io.ReadCloser, error)
	Delete(objectKey string) error
	GetURL(objectKey string) string
	IsEnabled() bool
}

// Save 保存文件到存储（兼容旧 API）
func Save(path string, data []byte) (string, error) {
	localStorage := &LocalStorage{Root: "uploads"}
	return localStorage.Save(path, data)
}

// SaveByMultipart 通过 multipart 表单保存文件（兼容旧 API）
func SaveByMultipart(path string, fileHeader *multipart.FileHeader) (string, error) {
	// 打开上传的文件
	file, err := fileHeader.Open()
	if err != nil {
		return "", err
	}
	defer file.Close()

	// 创建目标目录
	fullPath := filepath.Join("uploads", path)
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}

	// 创建目标文件
	dst, err := os.Create(fullPath)
	if err != nil {
		return "", err
	}
	defer dst.Close()

	// 复制文件内容
	if _, err := io.Copy(dst, file); err != nil {
		return "", err
	}

	return path, nil
}

// Delete 删除文件（兼容旧 API）
func Delete(path string) error {
	// 如果云存储启用，先尝试从云端删除
	storage := GetStorage()
	if storage.IsEnabled() {
		return storage.Delete(path)
	}

	// 本地存储删除
	fullPath := filepath.Join("uploads", path)
	return os.Remove(fullPath)
}

// CloudStorage 云存储管理器
type CloudStorage struct {
	provider StorageProvider
	enabled  bool
}

var (
	cloudStorage     *CloudStorage
	cloudStorageOnce sync.Once
)

// GetStorage 获取存储管理器单例
func GetStorage() *CloudStorage {
	cloudStorageOnce.Do(func() {
		cloudStorage = &CloudStorage{
			enabled: isCloudStorageEnabled(),
		}
		if cloudStorage.enabled {
			cloudStorage.provider = createProvider()
		}
	})
	return cloudStorage
}

func isCloudStorageEnabled() bool {
	storageType := getStorageType()
	return storageType == "s3" || storageType == "minio"
}

func getStorageType() string {
	if t, ok := config.C.Other["STORAGE_TYPE"]; ok {
		return strings.ToLower(t)
	}
	return "local"
}

func createProvider() StorageProvider {
	storageType := getStorageType()
	switch storageType {
	case "s3", "minio":
		return NewS3Provider()
	default:
		return nil
	}
}

// IsEnabled 检查是否启用
func (s *CloudStorage) IsEnabled() bool {
	return s.enabled && s.provider != nil && s.provider.IsEnabled()
}

// Upload 上传文件
func (s *CloudStorage) Upload(filePath string, objectKey string) (string, error) {
	if !s.IsEnabled() {
		// 本地存储，返回相对路径
		return filePath, nil
	}
	return s.provider.Upload(filePath, objectKey)
}

// UploadReader 上传 Reader
func (s *CloudStorage) UploadReader(reader io.Reader, objectKey string, size int64) (string, error) {
	if !s.IsEnabled() {
		return "", fmt.Errorf("storage not enabled")
	}
	return s.provider.UploadReader(reader, objectKey, size)
}

// Download 下载文件
func (s *CloudStorage) Download(objectKey string) (io.ReadCloser, error) {
	if !s.IsEnabled() {
		// 本地存储
		return os.Open(objectKey)
	}
	return s.provider.Download(objectKey)
}

// Delete 删除文件
func (s *CloudStorage) Delete(objectKey string) error {
	if !s.IsEnabled() {
		return os.Remove(objectKey)
	}
	return s.provider.Delete(objectKey)
}

// GetURL 获取文件 URL
func (s *CloudStorage) GetURL(objectKey string) string {
	if !s.IsEnabled() {
		return config.C.AppBaseURL + "/" + objectKey
	}
	return s.provider.GetURL(objectKey)
}

// S3Provider S3/MinIO 存储提供者
type S3Provider struct {
	client        *minio.Client
	bucket        string
	endpoint      string
	region        string
	useSSL        bool
	publicBaseURL string
}

// NewS3Provider 创建 S3 提供者
func NewS3Provider() *S3Provider {
	p := &S3Provider{
		bucket:        getEnvOrDefault("S3_BUCKET", "community"),
		endpoint:      getEnvOrDefault("S3_ENDPOINT", "localhost:9000"),
		region:        getEnvOrDefault("S3_REGION", "us-east-1"),
		useSSL:        getEnvOrDefault("S3_USE_SSL", "false") == "true",
		publicBaseURL: getEnvOrDefault("S3_PUBLIC_URL", ""),
	}

	accessKey := getEnvOrDefault("S3_ACCESS_KEY", "")
	secretKey := getEnvOrDefault("S3_SECRET_KEY", "")

	if accessKey == "" || secretKey == "" {
		log.Warn("S3 credentials not configured, storage disabled")
		return p
	}

	client, err := minio.New(p.endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: p.useSSL,
		Region: p.region,
	})
	if err != nil {
		log.Error("Failed to create S3 client: %v", err)
		return p
	}

	p.client = client

	// 确保 bucket 存在
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	exists, err := client.BucketExists(ctx, p.bucket)
	if err != nil {
		log.Error("Failed to check bucket existence: %v", err)
		return p
	}

	if !exists {
		err = client.MakeBucket(ctx, p.bucket, minio.MakeBucketOptions{Region: p.region})
		if err != nil {
			log.Error("Failed to create bucket: %v", err)
			return p
		}
		log.Info("Created S3 bucket: %s", p.bucket)
	}

	// 设置 bucket 策略为公开读取
	policy := fmt.Sprintf(`{
		"Version": "2012-10-17",
		"Statement": [{
			"Effect": "Allow",
			"Principal": {"AWS": "*"},
			"Action": ["s3:GetObject"],
			"Resource": ["arn:aws:s3:::%s/*"]
		}]
	}`, p.bucket)

	err = client.SetBucketPolicy(ctx, p.bucket, policy)
	if err != nil {
		log.Warn("Failed to set bucket policy: %v", err)
	}

	log.Info("S3 storage configured successfully")
	return p
}

func getEnvOrDefault(key, defaultValue string) string {
	if val, ok := config.C.Other[key]; ok {
		return val
	}
	return defaultValue
}

// IsEnabled 检查是否启用
func (p *S3Provider) IsEnabled() bool {
	return p.client != nil
}

// Upload 上传文件
func (p *S3Provider) Upload(filePath string, objectKey string) (string, error) {
	if !p.IsEnabled() {
		return "", fmt.Errorf("s3 client not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 获取文件信息
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return "", err
	}

	// 打开文件
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	// 上传
	_, err = p.client.PutObject(ctx, p.bucket, objectKey, file, fileInfo.Size(), minio.PutObjectOptions{
		ContentType: getContentType(filePath),
	})
	if err != nil {
		return "", err
	}

	return p.GetURL(objectKey), nil
}

// UploadReader 上传 Reader
func (p *S3Provider) UploadReader(reader io.Reader, objectKey string, size int64) (string, error) {
	if !p.IsEnabled() {
		return "", fmt.Errorf("s3 client not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	_, err := p.client.PutObject(ctx, p.bucket, objectKey, reader, size, minio.PutObjectOptions{
		ContentType: getContentType(objectKey),
	})
	if err != nil {
		return "", err
	}

	return p.GetURL(objectKey), nil
}

// Download 下载文件
func (p *S3Provider) Download(objectKey string) (io.ReadCloser, error) {
	if !p.IsEnabled() {
		return nil, fmt.Errorf("s3 client not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	object, err := p.client.GetObject(ctx, p.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}

	return object, nil
}

// Delete 删除文件
func (p *S3Provider) Delete(objectKey string) error {
	if !p.IsEnabled() {
		return fmt.Errorf("s3 client not initialized")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return p.client.RemoveObject(ctx, p.bucket, objectKey, minio.RemoveObjectOptions{})
}

// GetURL 获取文件 URL
func (p *S3Provider) GetURL(objectKey string) string {
	if p.publicBaseURL != "" {
		return p.publicBaseURL + "/" + p.bucket + "/" + objectKey
	}

	// 构建 URL
	scheme := "http"
	if p.useSSL {
		scheme = "https"
	}

	return fmt.Sprintf("%s://%s/%s/%s", scheme, p.endpoint, p.bucket, objectKey)
}

// getContentType 获取内容类型
func getContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	default:
		return "application/octet-stream"
	}
}

// GeneratePresignedURL 生成预签名 URL（用于临时访问）
func (s *CloudStorage) GeneratePresignedURL(objectKey string, expiry time.Duration) (string, error) {
	if !s.IsEnabled() {
		return "", fmt.Errorf("storage not enabled")
	}

	if provider, ok := s.provider.(*S3Provider); ok && provider.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		reqParams := make(url.Values)
		presignedURL, err := provider.client.PresignedGetObject(ctx, provider.bucket, objectKey, expiry, reqParams)
		if err != nil {
			return "", err
		}

		return presignedURL.String(), nil
	}

	return "", fmt.Errorf("presigned URL not supported")
}

// HealthCheck 健康检查
func (s *CloudStorage) HealthCheck() bool {
	if !s.IsEnabled() {
		return false
	}

	if provider, ok := s.provider.(*S3Provider); ok && provider.client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := provider.client.BucketExists(ctx, provider.bucket)
		return err == nil
	}

	return true
}
