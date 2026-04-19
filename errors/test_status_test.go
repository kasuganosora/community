package errors

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStatusCodeDirect(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("DELETE", "/test", nil)
	c.Status(http.StatusNoContent)
	t.Logf("w.Code = %d (expected 204)", w.Code)
	t.Logf("w.Body = %s", w.Body.String())
}
