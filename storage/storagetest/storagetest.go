// Package storagetest provides storage backends for tests: an in-process
// fake object store and a helper that connects to a real S3 compatible
// server (MinIO, versitygw, ...) configured through TEST_S3_* variables.
package storagetest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasuganosora/community/storage"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type object struct {
	data        []byte
	contentType string
	modTime     time.Time
}

// Memory is an in-process object store that behaves like the S3 backend
// (Kind "s3", no local files).
type Memory struct {
	mu      sync.Mutex
	objects map[string]object
	// PublicBase, when set, is returned by PublicURL as <PublicBase>/<key>.
	PublicBase string
	// Fail makes every operation (and Check) return this error.
	Fail error
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{objects: map[string]object{}} }

// Kind implements storage.Backend.
func (m *Memory) Kind() string { return storage.KindS3 }

// Put implements storage.Backend.
func (m *Memory) Put(_ context.Context, key string, r io.Reader, _ int64, contentType string) error {
	if m.Fail != nil {
		return m.Fail
	}
	k, err := storage.CleanKey(key)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.objects[k] = object{data: data, contentType: contentType, modTime: time.Now()}
	m.mu.Unlock()
	return nil
}

type readSeekCloser struct{ *bytes.Reader }

func (readSeekCloser) Close() error { return nil }

// Open implements storage.Backend.
func (m *Memory) Open(_ context.Context, key string) (io.ReadCloser, storage.ObjectInfo, error) {
	if m.Fail != nil {
		return nil, storage.ObjectInfo{}, m.Fail
	}
	k, err := storage.CleanKey(key)
	if err != nil {
		return nil, storage.ObjectInfo{}, err
	}
	m.mu.Lock()
	o, ok := m.objects[k]
	m.mu.Unlock()
	if !ok {
		return nil, storage.ObjectInfo{}, storage.ErrNotFound
	}
	return readSeekCloser{bytes.NewReader(o.data)}, storage.ObjectInfo{Size: int64(len(o.data)), ContentType: o.contentType, ModTime: o.modTime}, nil
}

// Stat implements storage.Backend.
func (m *Memory) Stat(ctx context.Context, key string) (storage.ObjectInfo, error) {
	rc, info, err := m.Open(ctx, key)
	if err == nil {
		_ = rc.Close()
	}
	return info, err
}

// Delete implements storage.Backend.
func (m *Memory) Delete(_ context.Context, key string) error {
	if m.Fail != nil {
		return m.Fail
	}
	k, err := storage.CleanKey(key)
	if err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.objects, k)
	m.mu.Unlock()
	return nil
}

// PublicURL implements storage.Backend.
func (m *Memory) PublicURL(key string) string {
	if m.PublicBase == "" {
		return ""
	}
	return strings.TrimRight(m.PublicBase, "/") + "/" + (&url.URL{Path: key}).EscapedPath()
}

// Check implements storage.Backend.
func (m *Memory) Check(context.Context) error { return m.Fail }

// List implements storage.Backend.
func (m *Memory) List(_ context.Context, prefix string, fn func(storage.ObjectInfo) error) error {
	if m.Fail != nil {
		return m.Fail
	}
	for seg := range strings.SplitSeq(prefix, "/") {
		if seg == ".." {
			return storage.ErrInvalidKey
		}
	}
	m.mu.Lock()
	infos := make([]storage.ObjectInfo, 0, len(m.objects))
	for k, o := range m.objects {
		if strings.HasPrefix(k, prefix) {
			infos = append(infos, storage.ObjectInfo{Key: k, Size: int64(len(o.data)), ContentType: o.contentType, ModTime: o.modTime})
		}
	}
	m.mu.Unlock()
	sort.Slice(infos, func(i, j int) bool { return infos[i].Key < infos[j].Key })
	for _, info := range infos {
		if err := fn(info); err != nil {
			if errors.Is(err, storage.ErrStopList) {
				return nil
			}
			return err
		}
	}
	return nil
}

// SetModTime backdates (or forwards) an object's modification time.
func (m *Memory) SetModTime(key string, t time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if o, ok := m.objects[key]; ok {
		o.modTime = t
		m.objects[key] = o
	}
}

// Keys lists stored keys (sorted).
func (m *Memory) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.objects))
	for k := range m.objects {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// S3Env describes a real S3 test server.
type S3Env struct {
	Endpoint, AccessKey, SecretKey, Region string
	UseSSL                                 bool
}

// S3FromEnv returns the TEST_S3_ENDPOINT / TEST_S3_ACCESS_KEY /
// TEST_S3_SECRET_KEY (optional TEST_S3_REGION, TEST_S3_USE_SSL) server, or
// skips the test when unset.
func S3FromEnv(t testing.TB) S3Env {
	t.Helper()
	env := S3Env{Endpoint: os.Getenv("TEST_S3_ENDPOINT"), AccessKey: os.Getenv("TEST_S3_ACCESS_KEY"), SecretKey: os.Getenv("TEST_S3_SECRET_KEY"), Region: os.Getenv("TEST_S3_REGION"), UseSSL: os.Getenv("TEST_S3_USE_SSL") == "true"}
	if env.Endpoint == "" || env.AccessKey == "" || env.SecretKey == "" {
		t.Skip("TEST_S3_ENDPOINT/TEST_S3_ACCESS_KEY/TEST_S3_SECRET_KEY not set")
	}
	if env.Region == "" {
		env.Region = "us-east-1"
	}
	return env
}

// NewBucket creates a fresh bucket on the test server, removes it (with its
// objects) on cleanup and returns its name.
func (e S3Env) NewBucket(t testing.TB) string {
	t.Helper()
	client, err := minio.New(e.Endpoint, &minio.Options{Creds: credentials.NewStaticV4(e.AccessKey, e.SecretKey, ""), Secure: e.UseSSL, Region: e.Region, BucketLookup: minio.BucketLookupPath})
	if err != nil {
		t.Fatalf("s3 client: %v", err)
	}
	name := fmt.Sprintf("community-test-%d", time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.MakeBucket(ctx, name, minio.MakeBucketOptions{Region: e.Region}); err != nil {
		t.Fatalf("make bucket: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for obj := range client.ListObjects(ctx, name, minio.ListObjectsOptions{Recursive: true}) {
			if obj.Err == nil {
				_ = client.RemoveObject(ctx, name, obj.Key, minio.RemoveObjectOptions{})
			}
		}
		_ = client.RemoveBucket(ctx, name)
	})
	return name
}

// Config returns an S3Config for bucket on the test server (no legacy
// local fallback).
func (e S3Env) Config(bucket string) storage.S3Config {
	return storage.S3Config{Endpoint: e.Endpoint, Region: e.Region, Bucket: bucket, AccessKey: e.AccessKey, SecretKey: e.SecretKey, UseSSL: e.UseSSL, PathStyle: "true"}
}
