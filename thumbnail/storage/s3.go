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

// S3Backend S3存储后端.
type S3Backend struct {
	client     *s3.Client
	bucket     string
	pathPrefix string
	publicURL  string
	region     string
}

// NewS3Backend 创建S3存储后端.
func NewS3Backend(cfg Config) (*S3Backend, error) {
	// 加载AWS配置
	awsCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKey,
			cfg.SecretKey,
			"")),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg)

	return &S3Backend{
		client:     client,
		bucket:     cfg.Bucket,
		pathPrefix: cfg.PathPrefix,
		publicURL:  cfg.PublicURL,
		region:     cfg.Region,
	}, nil
}

// Type 存储类型.
func (s *S3Backend) Type() string {
	return "s3"
}

// Put 上传文件.
func (s *S3Backend) Put(ctx context.Context, key string, data io.Reader, size int64, contentType string) error {
	fullKey := s.pathPrefix + key

	input := &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(fullKey),
		Body:        data,
		ContentType: aws.String(contentType),
	}

	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to upload to s3: %w", err)
	}

	return nil
}

// GetURL 获取访问URL.
func (s *S3Backend) GetURL(key string) string {
	fullKey := s.pathPrefix + key

	if s.publicURL != "" {
		return fmt.Sprintf("%s/%s", s.publicURL, fullKey)
	}

	// 使用S3 URL
	return fmt.Sprintf("https://%s.s3.%s.amazonaws.com/%s", s.bucket, s.region, fullKey)
}

// GetPresignedURL 获取预签名URL（用于私有bucket）.
func (s *S3Backend) GetPresignedURL(ctx context.Context, key string, expire time.Duration) (string, error) {
	fullKey := s.pathPrefix + key

	presignClient := s3.NewPresignClient(s.client)

	req, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(fullKey),
	}, s3.WithPresignExpires(expire))
	if err != nil {
		return "", fmt.Errorf("failed to generate presigned url: %w", err)
	}

	return req.URL, nil
}

// Exists 检查是否存在.
func (s *S3Backend) Exists(ctx context.Context, key string) (bool, error) {
	fullKey := s.pathPrefix + key

	input := &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(fullKey),
	}

	_, err := s.client.HeadObject(ctx, input)
	if err != nil {
		// 检查是否是NotFound错误
		return false, nil //nolint:nilerr
	}

	return true, nil
}

// Delete 删除.
func (s *S3Backend) Delete(ctx context.Context, key string) error {
	fullKey := s.pathPrefix + key

	input := &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(fullKey),
	}

	_, err := s.client.DeleteObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to delete from s3: %w", err)
	}

	return nil
}

// ListObjects 列出对象（用于清理）.
func (s *S3Backend) ListObjects(ctx context.Context, prefix string, maxKeys int32) ([]string, error) {
	fullPrefix := s.pathPrefix + prefix

	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(s.bucket),
		Prefix:  aws.String(fullPrefix),
		MaxKeys: aws.Int32(maxKeys),
	}

	result, err := s.client.ListObjectsV2(ctx, input)
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
