package controllers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestGetAccount(t *testing.T) {
	t.Run("account present in context", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		account := &models.Account{Id: 42}
		c.Set("account", account)

		result := GetAccount(c)
		assert.NotNil(t, result)
		assert.Equal(t, uint64(42), result.Id)
	})

	t.Run("no account in context", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		result := GetAccount(c)
		assert.Nil(t, result)
	})
}

func TestMustGetAccount(t *testing.T) {
	t.Run("account present", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		account := &models.Account{Id: 42}
		c.Set("account", account)

		result := MustGetAccount(c)
		assert.NotNil(t, result)
		assert.Equal(t, uint64(42), result.Id)
	})

	t.Run("no account panics", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		assert.PanicsWithValue(t, abortSentinel, func() {
			MustGetAccount(c)
		})
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestAbortIf(t *testing.T) {
	t.Run("condition false does nothing", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		// Should not panic when condition is false
		AbortIf(c, false, 400, gin.H{"error": "bad"}) //nolint:goconst
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("condition true with data panics", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		assert.PanicsWithValue(t, abortSentinel, func() {
			AbortIf(c, true, 400, gin.H{"error": "bad request"})
		})
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("condition true without data panics", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		assert.PanicsWithValue(t, abortSentinel, func() {
			AbortIf(c, true, 403, nil)
		})
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}

func TestAbortIfError(t *testing.T) {
	t.Run("nil error does nothing", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		AbortIfError(c, nil) // should not panic
		assert.Equal(t, http.StatusOK, w.Code)
	})
}

// TestAbortRecoveryMiddleware_ProductionOrder verifies that the production
// middleware ordering (gin.Recovery() outermost, AbortRecoveryMiddleware innermost)
// correctly swallows the AbortSentinel control-flow panic while preserving the
// already-written HTTP response. This is the cross-module contract between
// controllers (which call AbortPanic) and the server bootstrap (which registers
// AbortRecoveryMiddleware inside SetRouter).
func TestAbortRecoveryMiddleware_ProductionOrder(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	app := gin.New()
	// Simulate production: gin.Recovery is registered first (outermost).
	app.Use(gin.Recovery())
	// AbortRecoveryMiddleware is registered later (innermost), mirroring how
	// SetRouter registers it after main.go's global middleware.
	app.Use(AbortRecoveryMiddleware())
	app.GET("/test", func(c *gin.Context) {
		c.JSON(404, gin.H{"error": "not found"})
		AbortPanic()
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	app.ServeHTTP(w, req)

	assert.Equal(t, 404, w.Code, "response should be preserved (404), not replaced by gin.Recovery's 500")
	assert.Equal(t, `{"error":"not found"}`, w.Body.String())
}

// TestAbortRecoveryMiddleware_NonSentinelRethrown verifies that real panics
// are NOT swallowed — they must propagate to gin.Recovery for proper handling.
func TestAbortRecoveryMiddleware_NonSentinelRethrown(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	app := gin.New()
	app.Use(gin.Recovery())            // catches re-panicked real errors
	app.Use(AbortRecoveryMiddleware()) // must re-panic non-sentinel values
	app.GET("/boom", func(c *gin.Context) {
		panic("a real error, not a control-flow sentinel")
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/boom", nil)
	assert.NotPanics(t, func() {
		app.ServeHTTP(w, req) // gin.Recovery catches the re-panic, returns 500
	})
	assert.Equal(t, 500, w.Code)
}
