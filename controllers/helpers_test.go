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

		assert.PanicsWithValue(t, "abort", func() {
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

		assert.PanicsWithValue(t, "abort", func() {
			AbortIf(c, true, 400, gin.H{"error": "bad request"})
		})
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("condition true without data panics", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		assert.PanicsWithValue(t, "abort", func() {
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
