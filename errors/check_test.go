package errors

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCheckStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/test", nil) //nolint:noctx
	c.Status(http.StatusNoContent)
	fmt.Printf("w.Code = %d\n", w.Code)
}
