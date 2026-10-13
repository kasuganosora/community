package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWebGroupTransferLimitsExactRoutesAndConfiguration(t *testing.T) {
	const uploadPath = "/api/v1/chat/groups/IWdyb3Vw/uploads"
	req := httptest.NewRequest(http.MethodPost, uploadPath, nil)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=upload")
	for _, tc := range []struct {
		value string
		limit int64
	}{{"", 50 << 20}, {"10485760", 10 << 20}, {"100", 100}, {"999999999999", 50 << 20}, {"-1", 50 << 20}, {"invalid", 50 << 20}} {
		t.Setenv("MATRIX_WEB_GROUP_UPLOAD_MAX_BYTES", tc.value)
		require.Equal(t, tc.limit+multipartOverhead, bodyLimitFor(req, 10<<20, func() int64 { return 40 << 20 }))
	}
	for _, tc := range []struct{ method, path string }{
		{"POST", uploadPath + "/extra"}, {"POST", uploadPath + "other"}, {"PUT", uploadPath},
		{"POST", "/api/v1/chat/groups//uploads"}, {"POST", "/api/v1/chat/groups/../uploads"},
		{"POST", "/api/v1/chat/groups/IWdyb3Vw/messages"}, {"POST", "/api/v1/chat/group-sync"},
	} {
		r := httptest.NewRequest(tc.method, tc.path, nil)
		r.Header.Set("Content-Type", "application/json")
		require.EqualValues(t, 10<<20, bodyLimitFor(r, 10<<20, func() int64 { return 40 << 20 }), tc)
	}
	// Merely posting JSON to an upload route does not raise the JSON budget.
	req.Header.Set("Content-Type", "application/json")
	require.EqualValues(t, 10<<20, bodyLimitFor(req, 10<<20, func() int64 { return 40 << 20 }))
}

func TestWebGroupUploadBodyLimitIsAppliedToChunkedRequests(t *testing.T) {
	t.Setenv("MATRIX_WEB_GROUP_UPLOAD_MAX_BYTES", "100")
	r := gin.New()
	r.Use(BodySizeLimitMiddleware(10<<20, func() int64 { return 40 << 20 }))
	r.POST("/api/v1/chat/groups/:id/uploads", func(c *gin.Context) {
		_, err := io.Copy(io.Discard, c.Request.Body)
		if err != nil {
			c.Status(413)
			return
		}
		c.Status(204)
	})
	for _, size := range []int64{100, 101 + multipartOverhead} {
		req := httptest.NewRequest("POST", "/api/v1/chat/groups/IWdyb3Vw/uploads", strings.NewReader(strings.Repeat("x", int(size))))
		req.ContentLength = -1
		req.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if size == 100 {
			require.Equal(t, 204, w.Code)
		} else {
			require.Equal(t, 413, w.Code)
		}
	}
}

func TestWebGroupTransferTimeoutsAndSocketDeadlines(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		budget       time.Duration
	}{
		{"POST", "/api/v1/chat/groups/id/uploads", GroupMediaHandlerTimeout},
		{"GET", "/api/v1/chat/groups/id/messages/event/media/mediaid", GroupMediaHandlerTimeout},
		{"GET", "/api/v1/chat/groups/id/avatar", GroupMediaHandlerTimeout},
		{"GET", "/api/v1/chat/groups/id/members/member/avatar", GroupMediaHandlerTimeout},
		{"GET", "/api/v1/chat/groups/directory-avatar", GroupMediaHandlerTimeout},
		{"POST", "/api/v1/chat/group-sync", GroupSyncHandlerTimeout},
		{"GET", "/api/v1/chat/group-sync", GroupSyncHandlerTimeout},
		{"POST", "/api/v1/chat/groups/id/messages", 30 * time.Second},
		{"POST", "/api/v1/chat/groups/id/messages/event/media/id", 30 * time.Second},
		{"GET", "/api/v1/chat/groups/id/messages/event/media/id/extra", 30 * time.Second},
		{"GET", "/api/v1/chat/groups/id/avatar/extra", 30 * time.Second},
		{"GET", "/api/v1/chat/groups/id/members/avatar", 30 * time.Second},
		{"GET", "/api/v1/chat/group-sync/other", 30 * time.Second},
		{"DELETE", "/api/v1/chat/group-sync", 30 * time.Second},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			r := gin.New()
			r.Use(TimeoutMiddleware(30 * time.Second))
			r.Handle(tc.method, tc.path, func(c *gin.Context) {
				deadline, ok := c.Request.Context().Deadline()
				require.True(t, ok)
				require.WithinDuration(t, time.Now().Add(tc.budget), deadline, time.Second)
				c.Status(204)
			})
			w := &transferDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
			r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			require.Equal(t, 204, w.Code)
			if tc.budget == 30*time.Second {
				require.True(t, w.read.IsZero())
				require.True(t, w.write.IsZero())
			} else {
				require.WithinDuration(t, time.Now().Add(tc.budget+5*time.Second), w.read, time.Second)
				require.Equal(t, w.read, w.write)
			}
		})
	}
}

func TestWebGroupTransferHonorsEarlierParentDeadline(t *testing.T) {
	r := gin.New()
	r.Use(TimeoutMiddleware(30 * time.Second))
	r.POST("/api/v1/chat/groups/:id/uploads", func(c *gin.Context) {
		deadline, ok := c.Request.Context().Deadline()
		require.True(t, ok)
		require.Less(t, time.Until(deadline), 6*time.Second)
		c.Status(204)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	w := &transferDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	r.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/chat/groups/id/uploads", nil).WithContext(ctx))
	require.Equal(t, 204, w.Code)
	require.Equal(t, deadline, w.read)
	require.Equal(t, deadline, w.write)
}
