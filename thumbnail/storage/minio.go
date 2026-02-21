package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// MinIOBackend MinIO存储后端
type MinIOBackend struct {
	client     *s3.Client
	bucket     string
	pathPrefix string
	publicURL  string
	endpoint   string
}

// NewMinIOBackend 创建MinIO存储后端
func NewMinIOBackend(cfg Config) (*MinIOBackend, error) {
	// 加载配置
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKey,
			cfg.SecretKey,
			"")),
		config.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					URL:           cfg.Endpoint,
					SigningRegion: cfg.Region,
				}, nil
			}),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load minio config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true // MinIO 需要使用 path-style
	})

	return &MinIOBackend{
		client:     client,
		bucket:     cfg.Bucket,
		pathPrefix: cfg.PathPrefix,
		publicURL:  cfg.PublicURL,
		endpoint:   cfg.Endpoint,
	}, nil
}

// Type 存储类型
func (m *MinIOBackend) Type() string {
	return "minio"
}

// Put 上传文件
func (m *MinIOBackend) Put(ctx context.Context, key string, data io.Reader, size int64, contentType string) error {
	fullKey := m.pathPrefix + key

	input := &s3.PutObjectInput{
		Bucket:      aws.String(m.bucket),
		Key:         aws.String(fullKey),
		Body:        data,
		ContentType: aws.String(contentType),
	}

	_, err := m.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to upload to minio: %w", err)
	}

	return nil
}

// GetURL 获取访问URL
func (m *MinIOBackend) GetURL(key string) string {
	fullKey := m.pathPrefix + key

	if m.publicURL != "" {
		return fmt.Sprintf("%s/%s", m.publicURL, fullKey)
	}

	// 使用MinIO URL
	return fmt.Sprintf("%s/%s/%s", m.endpoint, m.bucket, fullKey)
}

// GetPresignedURL 获取预签名URL
func (m *MinIOBackend) GetPresignedURL(ctx context.Context, key string, expire time.Duration) (string, error) {
	fullKey := m.pathPrefix + key

	presignClient := s3.NewPresignClient(m.client)
	req, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(fullKey),
	}, s3.WithPresignExpires(expire))

	if err != nil {
		return "", fmt.Errorf("failed to generate presigned url: %w", err)
	}

	return req.URL, nil
}

// Exists 检查是否存在
func (m *MinIOBackend) Exists(ctx context.Context, key string) (bool, error) {
	fullKey := m.pathPrefix + key

	input := &s3.HeadObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(fullKey),
	}

	_, err := m.client.HeadObject(ctx, input)
	if err != nil {
		return false, nil
	}
	return true, nil
}

// Delete 删除
func (m *MinIOBackend) Delete(ctx context.Context, key string) error {
	fullKey := m.pathPrefix + key

	input := &s3.DeleteObjectInput{
		Bucket: aws.String(m.bucket),
		Key:    aws.String(fullKey),
	}

	_, err := m.client.DeleteObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to delete from minio: %w", err)
	}

	return nil
}

// ListObjects 列出对象
func (m *MinIOBackend) ListObjects(ctx context.Context, prefix string, maxKeys int32) ([]string, error) {
	fullPrefix := m.pathPrefix + prefix

	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(m.bucket),
		Prefix:  aws.String(fullPrefix),
		MaxKeys: aws.Int32(maxKeys),
	}

	result, err := m.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to list objects: %w", err)
	}

	var keys []string
	for _, obj := range result.Contents {
		if obj.Key != nil {
			keys = append(keys, *obj.Key)
		}
	}

	return keys, nil
}
