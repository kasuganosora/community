package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMatrixSyncTimeoutBudget(t *testing.T) {
	for _, tc := range []struct {
		name, method, path string
		want               time.Duration
	}{
		{"v3 idle", http.MethodGet, "/_matrix/client/v3/sync?timeout=30000", 60 * time.Second},
		{"r0 idle", http.MethodGet, "/_matrix/client/r0/sync?timeout=30000", 60 * time.Second},
		{"untrusted timeout remains bounded", http.MethodGet, "/_matrix/client/v3/sync?timeout=999999999999999999999999", 60 * time.Second},
		{"ordinary route", http.MethodGet, "/api/v1/timelines/home?timeout=30000", 30 * time.Second},
		{"matrix login", http.MethodGet, "/_matrix/client/v3/login", 30 * time.Second},
		{"wrong method", http.MethodPost, "/_matrix/client/v3/sync?timeout=30000", 30 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(TimeoutMiddleware(30 * time.Second))
			request := httptest.NewRequest(tc.method, tc.path, nil)
			var remaining time.Duration
			router.Handle(tc.method, request.URL.Path, func(c *gin.Context) {
				deadline, ok := c.Request.Context().Deadline()
				if ok {
					remaining = time.Until(deadline)
				}
				c.Status(http.StatusNoContent)
			})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusNoContent, recorder.Code)
			require.InDelta(t, tc.want.Seconds(), remaining.Seconds(), 1)
		})
	}
}

func TestMatrixSyncCanOutliveOrdinaryHandlerTimeout(t *testing.T) {
	router := gin.New()
	router.Use(TimeoutMiddleware(time.Millisecond))
	router.GET("/_matrix/client/v3/sync", func(c *gin.Context) {
		select {
		case <-time.After(15 * time.Millisecond):
			c.JSON(http.StatusOK, gin.H{"next_batch": "idle"})
		case <-c.Request.Context().Done():
			return
		}
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/_matrix/client/v3/sync?timeout=15", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "idle")
}
