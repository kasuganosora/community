package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBucketSuffixPublicBase(t *testing.T) {
	stripped, ok := bucketSuffixPublicBase("https://cdn.example/files-bucket", "files-bucket", "acct.r2.example")
	require.True(t, ok)
	assert.Equal(t, "https://cdn.example", stripped)

	stripped, ok = bucketSuffixPublicBase("https://cdn.example/assets/files-bucket/", "files-bucket", "https://acct.r2.example")
	require.True(t, ok)
	assert.Equal(t, "https://cdn.example/assets", stripped)

	// The API host itself is path-style (https://endpoint/bucket/key). Leave it.
	_, ok = bucketSuffixPublicBase("https://minio.example:9000/files-bucket", "files-bucket", "minio.example:9000")
	assert.False(t, ok)

	_, ok = bucketSuffixPublicBase("https://cdn.example", "files-bucket", "acct.r2.example")
	assert.False(t, ok)

	_, ok = bucketSuffixPublicBase("https://cdn.example/other", "files-bucket", "acct.r2.example")
	assert.False(t, ok)
}

func TestPublicURLOmitsBucketSuffixWhenThatPathDoesNotServeObjects(t *testing.T) {
	b := NewS3Backend(S3Config{
		Endpoint:  "https://acct.r2.example",
		Bucket:    "files-bucket",
		AccessKey: "ak",
		SecretKey: "sk",
		PublicURL: "https://cdn.example/files-bucket",
	})
	b.publicReachable = func(rawURL string) (bool, bool) {
		if strings.Contains(rawURL, "/files-bucket/") {
			return false, true
		}
		return true, true
	}

	assert.Equal(t, "https://cdn.example/media/2026/10/31/a.png", b.PublicURL("media/2026/10/31/a.png"))
	// Resolved once; a later call does not probe again.
	b.publicReachable = func(string) (bool, bool) {
		t.Fatal("public base probed twice")
		return false, false
	}
	assert.Equal(t, "https://cdn.example/media/2026/10/31/thumb_a.png", b.PublicURL("media/2026/10/31/thumb_a.png"))
}

func TestPublicURLKeepsBucketSuffixWhenThatPathServesObjects(t *testing.T) {
	b := NewS3Backend(S3Config{
		Endpoint:  "https://acct.r2.example",
		Bucket:    "files-bucket",
		AccessKey: "ak",
		SecretKey: "sk",
		PublicURL: "https://cdn.example/files-bucket",
	})
	var probed []string
	b.publicReachable = func(rawURL string) (bool, bool) {
		probed = append(probed, rawURL)
		return true, true
	}

	assert.Equal(t, "https://cdn.example/files-bucket/media/a.png", b.PublicURL("media/a.png"))
	require.NotEmpty(t, probed)
	assert.True(t, strings.HasPrefix(probed[0], "https://cdn.example/files-bucket/"))
}

func TestPublicURLDoesNotProbePathStyleOnTheAPIHost(t *testing.T) {
	b := NewS3Backend(S3Config{
		Endpoint:  "minio.example:9000",
		Bucket:    "files-bucket",
		AccessKey: "ak",
		SecretKey: "sk",
		UseSSL:    true,
		PublicURL: "https://minio.example:9000/files-bucket",
	})
	b.publicReachable = func(string) (bool, bool) {
		t.Fatal("path-style public URL on the API host must not be probed")
		return false, false
	}

	assert.Equal(t, "https://minio.example:9000/files-bucket/media/a.png", b.PublicURL("media/a.png"))
}

func TestFetchPublicStatusDistinguishesMissingAndServed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media/public-base-probe.txt" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))

			return
		}

		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	ok, sure := reachableFromStatus(fetchPublicStatus(context.Background(), srv.URL+"/files-bucket/media/public-base-probe.txt"))
	assert.False(t, ok)
	assert.True(t, sure)
	ok, sure = reachableFromStatus(fetchPublicStatus(context.Background(), srv.URL+"/media/public-base-probe.txt"))
	assert.True(t, ok)
	assert.True(t, sure)
}

func TestAlignPublicBaseRetriesAfterAnInconclusiveProbe(t *testing.T) {
	b := NewS3Backend(S3Config{
		Endpoint:  "https://acct.r2.example",
		Bucket:    "files-bucket",
		AccessKey: "ak",
		SecretKey: "sk",
		PublicURL: "https://cdn.example/files-bucket",
	})
	b.publicReachable = func(string) (bool, bool) { return false, false }
	for i := 0; i < 4; i++ {
		b.probeNotBefore = time.Now().Add(-time.Second)
		assert.Equal(t, "https://cdn.example/files-bucket/media/a.png", b.PublicURL("media/a.png"))
	}
	assert.Empty(t, b.resolvedPublicBase, "temporary failures must not permanently pin a broken URL")

	b.probeNotBefore = b.probeNotBefore.Add(-time.Hour)
	b.publicReachable = func(rawURL string) (bool, bool) {
		return !strings.Contains(rawURL, "/files-bucket/"), true
	}
	assert.Equal(t, "https://cdn.example/media/a.png", b.PublicURL("media/a.png"))
}

func TestPublicBaseProbeUsesRequestedObjectWithoutMutatingBucket(t *testing.T) {
	var methods, paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		paths = append(paths, r.URL.Path)
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.URL.Path == "/media/existing.png" {
			_, _ = w.Write([]byte("image"))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	b := NewS3Backend(S3Config{
		Endpoint: srv.URL, Bucket: "files", AccessKey: "read-only", SecretKey: "read-only",
		PublicURL: strings.Replace(srv.URL, "127.0.0.1", "localhost", 1) + "/files",
	})
	assert.Equal(t, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)+"/media/existing.png", b.PublicURL("media/existing.png"))
	assert.Equal(t, []string{"GET", "GET"}, methods)
	assert.Equal(t, []string{"/files/media/existing.png", "/media/existing.png"}, paths)
}
