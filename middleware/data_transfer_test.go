package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTransferUploadLimitAndExactEndpointBoundary(t *testing.T) {
	for _, path := range []string{"/api/v1/data/imports", "/api/v1/admin/data/imports"} {
		r := httptest.NewRequest("POST", path, nil)
		r.Header.Set("Content-Type", "multipart/form-data; boundary=test")
		limit := bodyLimitFor(r, 10<<20, func() int64 { return 20 << 20 })
		require.EqualValues(t, 257<<20, limit)
		router := gin.New()
		router.Use(BodySizeLimitMiddleware(10<<20, func() int64 { return 20 << 20 }))
		router.POST(path, func(c *gin.Context) { c.Status(204) })
		r.ContentLength = 256 << 20
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, 204, w.Code)
		r = httptest.NewRequest("POST", path, nil)
		r.Header.Set("Content-Type", "multipart/form-data; boundary=test")
		r.ContentLength = (257 << 20) + 1
		w = httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, 413, w.Code)
	}
	for _, path := range []string{"/api/v1/data/imports/evil", "/api/v1/data/importsXYZ", "/api/v1/data/export"} {
		r := httptest.NewRequest("POST", path, nil)
		r.Header.Set("Content-Type", "application/json")
		require.EqualValues(t, 10<<20, bodyLimitFor(r, 10<<20, func() int64 { return 20 << 20 }))
	}
}
func TestTransferTimeoutBudgetAndParentCancellation(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		expected     time.Duration
	}{{"POST", "/api/v1/data/imports", 5 * time.Minute}, {"POST", "/api/v1/admin/data/imports", 5 * time.Minute}, {"GET", "/api/v1/data/export", 5 * time.Minute}, {"GET", "/api/v1/data/imports", 30 * time.Second}, {"POST", "/api/v1/data/export", 30 * time.Second}, {"GET", "/api/v1/data/export/other", 30 * time.Second}} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		require.Equal(t, tc.expected, requestHandlerTimeout(r, 30*time.Second))
	}
	router := gin.New()
	router.Use(TimeoutMiddleware(time.Millisecond))
	router.GET("/api/v1/data/export", func(c *gin.Context) {
		deadline, ok := c.Request.Context().Deadline()
		require.True(t, ok)
		require.Greater(t, time.Until(deadline), 4*time.Minute)
		c.Status(204)
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/data/export", nil))
	require.Equal(t, 204, w.Code)
}

type transferDeadlineWriter struct {
	*httptest.ResponseRecorder
	read, write time.Time
}

func (w *transferDeadlineWriter) SetReadDeadline(t time.Time) error  { w.read = t; return nil }
func (w *transferDeadlineWriter) SetWriteDeadline(t time.Time) error { w.write = t; return nil }
func TestTransferSocketDeadlinesOnlyExtendExactRoutes(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		extend       bool
	}{{"GET", "/api/v1/data/export", true}, {"POST", "/api/v1/data/imports", true}, {"POST", "/api/v1/admin/data/imports", true}, {"GET", "/_matrix/client/v3/sync", false}, {"GET", "/api/v1/data/export/extra", false}, {"GET", "/api/v1/data/archive", false}} {
		router := gin.New()
		router.Use(TimeoutMiddleware(30 * time.Second))
		router.Handle(tc.method, tc.path, func(c *gin.Context) { c.Status(204) })
		w := &transferDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
		router.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		require.Equal(t, 204, w.Code)
		if tc.extend {
			require.WithinDuration(t, time.Now().Add(DataTransferHandlerTimeout+5*time.Second), w.read, time.Second)
			require.Equal(t, w.read, w.write)
		} else {
			require.True(t, w.read.IsZero())
			require.True(t, w.write.IsZero())
		}
	}
}
