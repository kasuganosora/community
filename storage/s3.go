package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config configures S3Backend. See .env.example for the matching
// environment variables.
type S3Config struct {
	Endpoint  string // host[:port]; a leading http:// or https:// sets UseSSL
	Region    string // "auto" for Cloudflare R2
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
	// PathStyle: "true" forces path-style requests (https://endpoint/bucket/key),
	// "false" forces virtual-host style, anything else lets minio-go decide.
	PathStyle string
	// PublicURL is an optional base URL that maps to the bucket root, e.g. an
	// R2 public bucket (https://pub-xxxx.r2.dev) or custom domain
	// (https://media.example.com). Object "media/a.jpg" is then served from
	// <PublicURL>/media/a.jpg. Leave empty for private buckets: the app streams
	// objects itself.
	PublicURL string
	// LegacyLocalRoot is a directory consulted read-only (and for deletes)
	// when an object is missing from the bucket, so files written before the
	// switch to S3 keep working. Empty disables the fallback.
	LegacyLocalRoot string
	// Transport overrides the HTTP transport (diagnostics, tests).
	Transport http.RoundTripper
}

// LoadS3Config reads S3_* settings from the configuration.
func LoadS3Config() S3Config {
	cfg := S3Config{
		Endpoint:        getEnvOrDefault("S3_ENDPOINT", "localhost:9000"),
		Region:          getEnvOrDefault("S3_REGION", "us-east-1"),
		Bucket:          getEnvOrDefault("S3_BUCKET", "community"),
		AccessKey:       getEnvOrDefault("S3_ACCESS_KEY", ""),
		SecretKey:       getEnvOrDefault("S3_SECRET_KEY", ""),
		UseSSL:          strings.EqualFold(getEnvOrDefault("S3_USE_SSL", "false"), "true"),
		PathStyle:       strings.ToLower(getEnvOrDefault("S3_FORCE_PATH_STYLE", "auto")),
		PublicURL:       strings.TrimRight(getEnvOrDefault("S3_PUBLIC_URL", ""), "/"),
		LegacyLocalRoot: "uploads",
	}
	if strings.EqualFold(getEnvOrDefault("S3_LEGACY_LOCAL_FALLBACK", "true"), "false") {
		cfg.LegacyLocalRoot = ""
	}

	return cfg
}

// currentS3Config returns the active S3 configuration (zero value in local mode).
func currentS3Config() S3Config {
	if b, ok := Default().(*S3Backend); ok {
		return b.cfg
	}

	return S3Config{}
}

func getEnvOrDefault(key, defaultValue string) string {
	if val, ok := config.C.Other[key]; ok && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}

	return defaultValue
}

// S3Backend stores objects in an S3 compatible bucket via minio-go.
type S3Backend struct {
	cfg       S3Config
	client    *minio.Client
	configErr error
	legacy    *LocalBackend
}

// NewS3Backend creates the backend. A configuration problem does not fall
// back to local storage: every operation then fails with the configuration
// error and the health check reports it.
func NewS3Backend(cfg S3Config) *S3Backend {
	endpoint := strings.TrimSpace(cfg.Endpoint)

	switch {
	case strings.HasPrefix(endpoint, "https://"):
		cfg.UseSSL = true
		endpoint = strings.TrimPrefix(endpoint, "https://")
	case strings.HasPrefix(endpoint, "http://"):
		cfg.UseSSL = false
		endpoint = strings.TrimPrefix(endpoint, "http://")
	}

	cfg.Endpoint = strings.TrimRight(endpoint, "/")
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	b := &S3Backend{cfg: cfg}

	if cfg.LegacyLocalRoot != "" {
		b.legacy = &LocalBackend{Root: cfg.LegacyLocalRoot}
	}

	switch {
	case cfg.Endpoint == "":
		b.configErr = errors.New("S3_ENDPOINT is not configured")
	case cfg.Bucket == "":
		b.configErr = errors.New("S3_BUCKET is not configured")
	case cfg.AccessKey == "" || cfg.SecretKey == "":
		b.configErr = errors.New("S3_ACCESS_KEY / S3_SECRET_KEY are not configured")
	}

	if b.configErr != nil {
		log.Error("S3 storage misconfigured: %v", b.configErr)

		return b
	}

	lookup := minio.BucketLookupAuto
	switch cfg.PathStyle {
	case "true", "1", "yes", "path":
		lookup = minio.BucketLookupPath
	case "false", "0", "no", "dns", "virtual":
		lookup = minio.BucketLookupDNS
	}

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       cfg.UseSSL,
		Region:       cfg.Region,
		BucketLookup: lookup,
		Transport:    cfg.Transport,
	})
	if err != nil {
		b.configErr = errors.New("invalid S3 endpoint configuration: " + b.sanitize(err))
		log.Error("S3 storage misconfigured: %v", b.configErr)

		return b
	}

	b.client = client
	log.Info("S3 storage configured (endpoint %s, bucket %s)", cfg.Endpoint, cfg.Bucket)

	return b
}

// Kind implements Backend.
func (b *S3Backend) Kind() string { return KindS3 }

// Config returns the backend configuration without credentials.
func (b *S3Backend) Config() S3Config {
	c := b.cfg
	c.AccessKey, c.SecretKey = "", ""

	return c
}

func (b *S3Backend) ready() error {
	if b.configErr != nil {
		return b.configErr
	}

	if b.client == nil {
		return errors.New("s3 client not initialized")
	}

	return nil
}

// Put implements Backend.
func (b *S3Backend) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if err := b.ready(); err != nil {
		return err
	}

	k, err := CleanKey(key)
	if err != nil {
		return err
	}

	if contentType == "" {
		contentType = getContentType(k)
	}

	_, err = b.client.PutObject(ctx, b.cfg.Bucket, k, r, size, minio.PutObjectOptions{
		ContentType:  contentType,
		CacheControl: "public, max-age=31536000, immutable",
	})
	if err != nil {
		return errors.New("s3 put failed: " + b.sanitize(err))
	}

	return nil
}

// Open implements Backend. Objects missing from the bucket are looked up in
// the legacy local directory when that fallback is enabled.
func (b *S3Backend) Open(ctx context.Context, key string) (io.ReadCloser, ObjectInfo, error) {
	k, err := CleanKey(key)
	if err != nil {
		return nil, ObjectInfo{}, err
	}

	if err := b.ready(); err != nil {
		return nil, ObjectInfo{}, err
	}

	obj, err := b.client.GetObject(ctx, b.cfg.Bucket, k, minio.GetObjectOptions{})
	if err == nil {
		st, statErr := obj.Stat()
		if statErr == nil {
			return obj, ObjectInfo{Size: st.Size, ContentType: st.ContentType, ModTime: st.LastModified}, nil
		}

		_ = obj.Close()
		err = statErr
	}

	if !isNotFound(err) {
		return nil, ObjectInfo{}, errors.New("s3 get failed: " + b.sanitize(err))
	}

	if b.legacy != nil {
		return b.legacy.Open(ctx, k)
	}

	return nil, ObjectInfo{}, ErrNotFound
}

// Stat implements Backend.
func (b *S3Backend) Stat(ctx context.Context, key string) (ObjectInfo, error) {
	k, err := CleanKey(key)
	if err != nil {
		return ObjectInfo{}, err
	}

	if err := b.ready(); err != nil {
		return ObjectInfo{}, err
	}

	st, err := b.client.StatObject(ctx, b.cfg.Bucket, k, minio.StatObjectOptions{})
	if err == nil {
		return ObjectInfo{Size: st.Size, ContentType: st.ContentType, ModTime: st.LastModified}, nil
	}

	if !isNotFound(err) {
		return ObjectInfo{}, errors.New("s3 stat failed: " + b.sanitize(err))
	}

	if b.legacy != nil {
		return b.legacy.Stat(ctx, k)
	}

	return ObjectInfo{}, ErrNotFound
}

// Delete implements Backend. It also removes a legacy local copy.
func (b *S3Backend) Delete(ctx context.Context, key string) error {
	k, err := CleanKey(key)
	if err != nil {
		return err
	}

	if b.legacy != nil {
		_ = b.legacy.Delete(ctx, k)
	}

	if err := b.ready(); err != nil {
		return err
	}

	if err := b.client.RemoveObject(ctx, b.cfg.Bucket, k, minio.RemoveObjectOptions{}); err != nil && !isNotFound(err) {
		return errors.New("s3 delete failed: " + b.sanitize(err))
	}

	return nil
}

// List implements Backend. Only bucket objects are listed; files that
// still live in the legacy local directory are not included.
func (b *S3Backend) List(ctx context.Context, prefix string, fn func(ObjectInfo) error) error {
	prefix = strings.TrimLeft(strings.TrimSpace(prefix), "/")
	if _, err := listPrefixDir(prefix); err != nil {
		return err
	}

	if err := b.ready(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	for obj := range b.client.ListObjects(ctx, b.cfg.Bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
		if obj.Err != nil {
			return errors.New("s3 list failed: " + b.sanitize(obj.Err))
		}
		if strings.HasSuffix(obj.Key, "/") {
			continue // directory marker
		}
		if err := fn(ObjectInfo{Key: obj.Key, Size: obj.Size, ContentType: obj.ContentType, ModTime: obj.LastModified}); err != nil {
			if errors.Is(err, ErrStopList) {
				return nil
			}
			return err
		}
	}

	return ctx.Err()
}

// PublicURL implements Backend.
func (b *S3Backend) PublicURL(key string) string {
	if b.cfg.PublicURL == "" {
		return ""
	}

	k, err := CleanKey(key)
	if err != nil {
		return ""
	}

	return b.cfg.PublicURL + "/" + (&url.URL{Path: k}).EscapedPath()
}

// PresignedURL returns a temporary GET URL for key.
func (b *S3Backend) PresignedURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if err := b.ready(); err != nil {
		return "", err
	}

	k, err := CleanKey(key)
	if err != nil {
		return "", err
	}

	u, err := b.client.PresignedGetObject(ctx, b.cfg.Bucket, k, expiry, url.Values{})
	if err != nil {
		return "", errors.New("s3 presign failed: " + b.sanitize(err))
	}

	return u.String(), nil
}

// Check implements Backend with a HeadBucket request (BucketExists). It
// fails on bad credentials, a missing bucket or an unreachable endpoint.
func (b *S3Backend) Check(ctx context.Context) error {
	if err := b.ready(); err != nil {
		return err
	}

	exists, err := b.client.BucketExists(ctx, b.cfg.Bucket)
	if err != nil {
		return errors.New("bucket check failed: " + b.sanitize(err))
	}

	if !exists {
		return fmt.Errorf("bucket %q does not exist", b.cfg.Bucket)
	}

	return nil
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}

	resp := minio.ToErrorResponse(err)

	return resp.Code == "NoSuchKey" || resp.StatusCode == http.StatusNotFound && resp.Code != "NoSuchBucket"
}

// sanitize renders err without credentials or signed query strings.
func (b *S3Backend) sanitize(err error) string {
	if err == nil {
		return ""
	}

	var msg string

	resp := minio.ToErrorResponse(err)
	switch {
	case resp.Code != "":
		msg = resp.Code
		if resp.Message != "" {
			msg += ": " + resp.Message
		}
	case resp.StatusCode != 0:
		msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
	default:
		msg = err.Error()
		var uerr *url.Error
		if errors.As(err, &uerr) {
			msg = uerr.Op + " " + b.cfg.Endpoint + ": " + uerr.Err.Error()
		}
	}

	for _, secret := range []string{b.cfg.SecretKey, b.cfg.AccessKey} {
		if len(secret) >= 4 {
			msg = strings.ReplaceAll(msg, secret, "[redacted]")
		}
	}

	if i := strings.Index(msg, "X-Amz-"); i >= 0 {
		msg = msg[:i] + "[redacted]"
	}

	return msg
}

// ---------------------------------------------------------------------------
// Cached health probe

// HealthReport is the cached result of probing the default backend.
type HealthReport struct {
	Backend   string        `json:"backend"`
	Bucket    string        `json:"bucket,omitempty"`
	Endpoint  string        `json:"endpoint,omitempty"`
	Healthy   bool          `json:"healthy"`
	Error     string        `json:"error,omitempty"`
	TimedOut  bool          `json:"timed_out,omitempty"`
	Latency   time.Duration `json:"-"`
	CheckedAt time.Time     `json:"checked_at"`
	Cached    bool          `json:"cached"`
}

var (
	healthMu    sync.Mutex
	healthCache *HealthReport
	// HealthCacheTTL is how long a probe result is reused.
	HealthCacheTTL = 30 * time.Second
	// HealthTimeout bounds a single probe.
	HealthTimeout = 4 * time.Second
)

func resetHealthCache() {
	healthMu.Lock()
	healthCache = nil
	healthMu.Unlock()
}

// CheckHealth probes the default backend (at most once per HealthCacheTTL).
func CheckHealth(ctx context.Context) HealthReport {
	healthMu.Lock()
	defer healthMu.Unlock()

	if healthCache != nil && time.Since(healthCache.CheckedAt) < HealthCacheTTL {
		r := *healthCache
		r.Cached = true

		return r
	}

	b := Default()
	r := HealthReport{Backend: b.Kind()}

	if s3b, ok := b.(*S3Backend); ok {
		r.Bucket = s3b.cfg.Bucket
		r.Endpoint = s3b.cfg.Endpoint
	}

	pctx, cancel := context.WithTimeout(ctx, HealthTimeout)
	defer cancel()

	start := time.Now()
	err := b.Check(pctx)
	r.Latency = time.Since(start)
	r.CheckedAt = time.Now()
	r.Healthy = err == nil

	if err != nil {
		r.Error = err.Error()
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(pctx.Err(), context.DeadlineExceeded) {
			r.Error = "storage probe timed out after " + HealthTimeout.String()
			r.TimedOut = true
		}
		// The report is exposed on a public endpoint: keep the endpoint host
		// (it can carry an account id) out of the message.
		if r.Endpoint != "" {
			host := strings.Split(r.Endpoint, ":")[0]
			r.Error = strings.ReplaceAll(strings.ReplaceAll(r.Error, r.Endpoint, "[endpoint]"), host, "[endpoint]")
		}
	}

	healthCache = &r

	return r
}
