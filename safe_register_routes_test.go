package community

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// A duplicate route inside a registration group must stop startup instead of
// silently dropping the group.
func TestSafeRegisterRoutesFailsFastOnDuplicate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := gin.New()
	app.GET("/dup", func(*gin.Context) {})
	require.Panics(t, func() {
		safeRegisterRoutes(func() { app.GET("/dup", func(*gin.Context) {}) })
	})
	require.NotPanics(t, func() {
		safeRegisterRoutes(func() { app.GET("/ok", func(*gin.Context) {}) })
	})
}
