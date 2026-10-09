package homeserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/middleware"
	"github.com/stretchr/testify/require"
)

type countingMediaReader struct {
	reads int
	body  io.Reader
}

func (r *countingMediaReader) Read(p []byte) (int, error) { r.reads++; return r.body.Read(p) }
func TestAsyncMediaOwnershipStreamingAndImmutableBytes(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&AsyncMediaInfo{}, &MatrixMedia{}))
	t.Chdir(t.TempDir())
	service := NewAsyncMediaService(&HomeServer{Config: &HSConfig{ServerName: "local.test"}})
	payload := []byte{0, 255, 4, 7, 0, 17, 23}
	pending, err := service.CreateAsyncUpload("@alice:local.test", "秘密.bin", "application/octet-stream", int64(len(payload)))
	require.NoError(t, err)
	source := &countingMediaReader{body: bytes.NewReader(payload)}
	_, err = service.CompleteAsyncUpload(context.Background(), pending.MediaID, "@bob:local.test", source, "")
	require.ErrorIs(t, err, ErrMediaForbidden)
	require.Zero(t, source.reads, "unauthorized uploads must not consume a body")
	response, err := service.CompleteAsyncUpload(context.Background(), pending.MediaID, "@alice:local.test", bytes.NewReader(payload), "application/octet-stream")
	require.NoError(t, err)
	require.Contains(t, response.ContentURI, pending.MediaID)
	row, r, err := NewMediaService(service.hs).GetMedia("local.test", pending.MediaID)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, payload, got)
	require.Equal(t, "秘密.bin", row.FileName)
	require.Equal(t, "@alice:local.test", row.UploaderID)
	var stored AsyncMediaInfo
	require.NoError(t, db.GetDB().Where("media_id = ?", pending.MediaID).First(&stored).Error)
	hash := sha256.Sum256(payload)
	require.Equal(t, hex.EncodeToString(hash[:]), stored.Sha256Hash)
	_, err = service.CompleteAsyncUpload(context.Background(), pending.MediaID, "@alice:local.test", strings.NewReader("overwrite"), "")
	require.ErrorIs(t, err, ErrCannotOverwriteMedia)
	original, err := os.ReadFile(filepath.Join("uploads", filepath.FromSlash(row.FilePath)))
	require.NoError(t, err)
	require.Equal(t, payload, original)
}
func TestAsyncMediaFailureDoesNotPublishPartialFile(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&AsyncMediaInfo{}, &MatrixMedia{}))
	t.Chdir(t.TempDir())
	service := NewAsyncMediaService(&HomeServer{Config: &HSConfig{ServerName: "local.test"}})
	p, err := service.CreateAsyncUpload("alice", "file.bin", "application/octet-stream", 4)
	require.NoError(t, err)
	_, err = service.CompleteAsyncUpload(context.Background(), p.MediaID, "alice", strings.NewReader("xx"), "")
	require.ErrorContains(t, err, "size mismatch")
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixMedia{}).Count(&n).Error)
	require.Zero(t, n)
	status, err := service.GetUploadStatus(p.MediaID)
	require.NoError(t, err)
	require.Equal(t, "pending", status.Status)
	_, err = service.CompleteAsyncUpload(context.Background(), p.MediaID, "alice", strings.NewReader("xxxx"), "")
	require.NoError(t, err)
	_, err = service.CreateAsyncUpload("alice", "large", "", matrixMediaLimit+1)
	require.ErrorIs(t, err, ErrMediaTooLarge)
}
func TestAsyncMediaStreamLimitStopsAtBoundary(t *testing.T) {
	for _, size := range []int{1023, 1024, 1025, 2048} {
		input := bytes.NewReader(make([]byte, size))
		n, err := copyAsyncMedia(io.Discard, input, 1024)
		if size > 1024 {
			require.ErrorIs(t, err, ErrMediaTooLarge)
			require.Equal(t, int64(1025), n)
			require.Equal(t, size-1025, input.Len())
		} else {
			require.NoError(t, err)
			require.Equal(t, int64(size), n)
		}
	}
}
func TestMediaFederationKeepsConnectionsWithinSigningIdentity(t *testing.T) {
	var connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Contains(t, r.Header.Get("Authorization"), `origin="origin.local"`)
		writeMockFederationMedia(w, "application/octet-stream", []byte{0, 255, 1})
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()
	client, host := newTLSFederationClient(server)
	hs := client.hs
	hs.mediaFederationOnce.Do(func() { hs.mediaFederationClient = client })
	for i := 0; i < 3; i++ {
		result, err := hs.mediaFederation().fetchAuthenticatedMedia(host, "opaque", "download", "")
		require.NoError(t, err)
		require.Equal(t, []byte{0, 255, 1}, result.Data)
	}
	require.Equal(t, int64(1), connections.Load(), "successive media requests must reuse one authenticated TLS connection")
	other := &HomeServer{Config: &HSConfig{ServerName: "other.local"}}
	require.NotSame(t, hs.mediaFederation(), other.mediaFederation())
}

func TestAsyncMediaRoutesRequireAuthentication(t *testing.T) {
	previous := GlobalHS
	defer func() { GlobalHS = previous }()
	GlobalHS = &HomeServer{Config: &HSConfig{Enabled: true, ServerName: "local.test"}}
	router := gin.New()
	RegisterAsyncMediaRoutes(router)
	for _, method := range []string{"GET", "PUT"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, "/_matrix/media/v3/upload/known-id", strings.NewReader("opaque")))
		require.Equal(t, 401, w.Code)
	}
}

type blockedMediaReader struct {
	started chan struct{}
	release chan struct{}
	done    bool
}

func (r *blockedMediaReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	close(r.started)
	<-r.release
	return copy(p, []byte("opaque")), nil
}
func TestAsyncMediaConcurrentCompletionCannotOverwrite(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&AsyncMediaInfo{}, &MatrixMedia{}))
	t.Chdir(t.TempDir())
	service := NewAsyncMediaService(&HomeServer{Config: &HSConfig{ServerName: "local.test"}})
	p, err := service.CreateAsyncUpload("alice", "file.bin", "application/octet-stream", 6)
	require.NoError(t, err)
	source := &blockedMediaReader{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := service.CompleteAsyncUpload(context.Background(), p.MediaID, "alice", source, "")
		done <- err
	}()
	<-source.started
	_, secondErr := service.CompleteAsyncUpload(context.Background(), p.MediaID, "alice", strings.NewReader("second"), "")
	close(source.release)
	require.NoError(t, <-done)
	require.ErrorIs(t, secondErr, ErrCannotOverwriteMedia)
	_, reader, err := NewMediaService(service.hs).GetMedia("local.test", p.MediaID)
	require.NoError(t, err)
	defer reader.Close()
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "opaque", string(data))
}

func TestFederationMediaStreamRejectsTruncationExtraPartsAndOversize(t *testing.T) {
	prefix := "--test\r\nContent-Type: application/json\r\n\r\n{}\r\n--test\r\nContent-Type: application/octet-stream\r\n\r\n"
	for _, test := range []struct {
		name, body string
		valid      bool
	}{
		{"exact", prefix + strings.Repeat("x", 1024) + "\r\n--test--\r\n", true},
		{"oversize", prefix + strings.Repeat("x", 1025) + "\r\n--test--\r\n", false},
		{"truncated", prefix + "content", false},
		{"missing", "--test\r\n\r\n{}\r\n--test--\r\n", false},
		{"extra", prefix + "content\r\n--test\r\n\r\nextra\r\n--test--\r\n", false},
		{"redirect", "--test\r\n\r\n{}\r\n--test\r\nLocation: https://example.com/file\r\n\r\n\r\n--test--\r\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseFederationMediaBounded("multipart/mixed; boundary=test", strings.NewReader(test.body), 1024)
			if test.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
func TestFederationMediaCancellationClosesRemoteResponse(t *testing.T) {
	started := make(chan struct{})
	closed := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "multipart/mixed; boundary=test")
		_, _ = io.WriteString(w, "--test\r\n\r\n{}\r\n--test\r\nContent-Type: application/octet-stream\r\n\r\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		close(closed)
	}))
	defer server.Close()
	client, host := newTLSFederationClient(server)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := client.fetchAuthenticatedMediaContext(ctx, host, "file", "download", "")
		result <- err
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		require.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("download ignored cancellation")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("remote response remained open")
	}
}

// The buffered baseline reproduces the previous transport ReadFrom + multipart
// ReadAll path; both cases parse the same 1 MiB opaque attachment and validate it.
func BenchmarkFederationMediaMultipartMemory(b *testing.B) {
	payload := bytes.Repeat([]byte{0, 255, 1, 7}, (1<<20)/4)
	prefix := []byte("--test\r\nContent-Type: application/json\r\n\r\n{}\r\n--test\r\nContent-Type: application/octet-stream\r\n\r\n")
	suffix := []byte("\r\n--test--\r\n")
	for _, buffered := range []bool{true, false} {
		name := "streamed"
		if buffered {
			name = "previous-buffered"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			for i := 0; i < b.N; i++ {
				var reader io.Reader = io.MultiReader(bytes.NewReader(prefix), bytes.NewReader(payload), bytes.NewReader(suffix))
				if buffered {
					var raw bytes.Buffer
					if _, err := raw.ReadFrom(reader); err != nil {
						b.Fatal(err)
					}
					reader = bytes.NewReader(raw.Bytes())
				}
				media, err := parseFederationMediaReader("multipart/mixed; boundary=test", reader)
				if err != nil {
					b.Fatal(err)
				}
				if !bytes.Equal(media.Data, payload) {
					b.Fatal("opaque payload changed")
				}
			}
		})
	}
}

func TestAsyncMediaExpiredClaimRecoveryFencesOldWriter(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&AsyncMediaInfo{}, &MatrixMedia{}))
	t.Chdir(t.TempDir())
	service := NewAsyncMediaService(&HomeServer{Config: &HSConfig{ServerName: "local.test"}})
	pending, err := service.CreateAsyncUpload("alice", "file.bin", "application/octet-stream", 6)
	require.NoError(t, err)
	oldSource := &blockedMediaReader{started: make(chan struct{}), release: make(chan struct{})}
	oldDone := make(chan error, 1)
	go func() {
		_, err := service.CompleteAsyncUpload(context.Background(), pending.MediaID, "alice", oldSource, "")
		oldDone <- err
	}()
	<-oldSource.started
	var claimed AsyncMediaInfo
	require.NoError(t, db.GetDB().Where("media_id = ?", pending.MediaID).First(&claimed).Error)
	require.NotEmpty(t, claimed.ClaimToken)
	require.NotNil(t, claimed.ClaimExpiresAt)
	// Simulate the durable state left by a crashed uploader, without waiting five minutes.
	require.NoError(t, db.GetDB().Model(&AsyncMediaInfo{}).Where("id = ?", claimed.ID).Update("claim_expires_at", time.Now().Add(-time.Minute)).Error)
	_, replacementErr := service.CompleteAsyncUpload(context.Background(), pending.MediaID, "alice", strings.NewReader("newest"), "")
	close(oldSource.release)
	oldErr := <-oldDone
	require.NoError(t, replacementErr)
	require.Error(t, oldErr, "the previous owner cannot publish after a takeover")
	var completed AsyncMediaInfo
	require.NoError(t, db.GetDB().Where("media_id = ?", pending.MediaID).First(&completed).Error)
	require.Equal(t, "done", completed.Status)
	require.Empty(t, completed.ClaimToken)
	_, reader, err := NewMediaService(service.hs).GetMedia("local.test", pending.MediaID)
	require.NoError(t, err)
	defer reader.Close()
	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, "newest", string(actual))
	files, err := filepath.Glob("uploads/matrix/*/*/*/async-*")
	require.NoError(t, err)
	require.Len(t, files, 1, "the losing attempt only removes its own temporary path")
}

type cancellableMediaReader struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (r *cancellableMediaReader) Read([]byte) (int, error) {
	close(r.started)
	<-r.closed
	return 0, context.Canceled
}
func (r *cancellableMediaReader) Close() error { r.once.Do(func() { close(r.closed) }); return nil }
func TestAsyncMediaCancellationClosesBodyAndReleasesOwnClaim(t *testing.T) {
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&AsyncMediaInfo{}, &MatrixMedia{}))
	t.Chdir(t.TempDir())
	service := NewAsyncMediaService(&HomeServer{Config: &HSConfig{ServerName: "local.test"}})
	pending, err := service.CreateAsyncUpload("alice", "file.bin", "application/octet-stream", 0)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &cancellableMediaReader{started: make(chan struct{}), closed: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := service.CompleteAsyncUpload(ctx, pending.MediaID, "alice", source, ""); done <- err }()
	<-source.started
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("upload body did not close")
	}
	row, err := service.GetUploadStatus(pending.MediaID)
	require.NoError(t, err)
	require.Equal(t, "pending", row.Status)
	require.Empty(t, row.ClaimToken)
	var n int64
	require.NoError(t, db.GetDB().Model(&MatrixMedia{}).Count(&n).Error)
	require.Zero(t, n)
}

func TestMatrixMediaCrossOriginHeadersDoNotBypassAuthentication(t *testing.T) {
	restore := resetGlobalHS(minimalHS("test.local"))
	t.Cleanup(restore)
	router := gin.New()
	router.Use(middleware.SecurityHeadersMiddleware())
	RegisterRoutes(router)
	for _, kind := range []string{"download", "thumbnail"} {
		authenticatedPath := "/_matrix/client/v1/media/" + kind + "/test.local/private-file"
		w := httptest.NewRecorder()
		request := httptest.NewRequest("GET", authenticatedPath, nil)
		request.Header.Set("Origin", "https://element.example")
		router.ServeHTTP(w, request)
		require.Equal(t, 401, w.Code)
		require.Equal(t, "cross-origin", w.Header().Get("Cross-Origin-Resource-Policy"))
		require.Contains(t, w.Body.String(), "M_MISSING_TOKEN")
		w = httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/_matrix/media/v3/"+kind+"/test.local/private-file", nil))
		require.Equal(t, 404, w.Code)
		require.Equal(t, "same-origin", w.Header().Get("Cross-Origin-Resource-Policy"))
	}
}
