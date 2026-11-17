package controllers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestControllerHelpersTreatClientCancelAs499WithoutUnhandledError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name string
		fn   func(*gin.Context)
	}{
		{
			name: "AbortIfError",
			fn: func(c *gin.Context) {
				AbortIfError(c, fmt.Errorf("timeline read: %w", context.Canceled))
			},
		},
		{
			name: "AbortIfNotFoundAndOtherError",
			fn: func(c *gin.Context) {
				AbortIfNotFoundAndOtherError(c, fmt.Errorf("lookup: %w", context.Canceled), "Account not found")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var capturedErrors int
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Next()
				capturedErrors = len(c.Errors)
			}, AbortRecoveryMiddleware())
			r.GET("/test", tc.fn)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest(http.MethodGet, "/test", nil).WithContext(ctx)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			require.Equal(t, 499, w.Code)
			require.Zero(t, capturedErrors, "client disconnect should not push unhandled error to c.Errors")
		})
	}
}
