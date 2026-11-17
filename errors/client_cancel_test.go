package errors

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/log"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestErrorHandlerTreatsClientDisconnectAsExpectedCancel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := log.GetLogObject()
	var logs bytes.Buffer
	oldOut := logger.Out
	oldLevel := logger.GetLevel()
	logger.SetOutput(&logs)
	logger.SetLevel(logrus.InfoLevel)
	t.Cleanup(func() {
		logger.SetOutput(oldOut)
		logger.SetLevel(oldLevel)
	})

	router := gin.New()
	router.Use(ErrorHandler())
	router.GET("/api/v1/timelines/home", func(c *gin.Context) {
		_ = c.Error(fmt.Errorf("timeline query: %w", context.Canceled))
	})
	router.GET("/api/v1/accounts/lookup", func(c *gin.Context) {
		c.Status(http.StatusOK)
		_, _ = c.Writer.Write([]byte(`{}`))
		_ = c.Error(context.Canceled)
	})

	for _, path := range []string{"/api/v1/timelines/home", "/api/v1/accounts/lookup"} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if path == "/api/v1/timelines/home" {
			require.Equal(t, StatusClientClosedRequest, w.Code)
		}
	}

	output := logs.String()
	require.NotContains(t, output, "Unhandled error")
	require.NotContains(t, output, "Request error")
	require.NotContains(t, output, "context canceled")

	// A context.Canceled error when the incoming client request context was NOT
	// canceled is an unexpected internal cancellation and must still be logged.
	logs.Reset()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/timelines/home", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Contains(t, logs.String(), "Unhandled error")
}
