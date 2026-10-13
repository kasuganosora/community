package homeserver

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestWebGroupReadErrorsRetainServerDiagnostics(t *testing.T) {
	for _, operation := range []string{"list", "details", "sync"} {
		t.Run(operation, func(t *testing.T) {
			_, conn, r := setupWebGroupCore(t)
			id := webGroupTestCreate(t, r)
			// A missing read dependency reproduces a database failure without
			// exposing it in the client-facing response.
			require.NoError(t, conn.Migrator().DropTable(&MatrixReceipt{}))
			var diagnostics string
			r.Use(func(c *gin.Context) { c.Next(); diagnostics = c.Errors.String() })
			var handler gin.HandlerFunc
			switch operation {
			case "list":
				handler = handleWebGroupList
			case "details":
				handler = handleWebGroupGet
			case "sync":
				handler = handleWebGroupSync
			}
			r.GET("/diagnostics/:id", func(c *gin.Context) {
				c.Set("user_id", "@alice:test.local")
				c.Set("web_group_identity", true)
				handler(c)
			})
			response := httptest.NewRecorder()
			r.ServeHTTP(response, httptest.NewRequest("GET", "/diagnostics/"+id, nil))
			require.Equal(t, 500, response.Code)
			require.Contains(t, diagnostics, "web groups")
			require.Contains(t, diagnostics, "matrix_receipts")
			require.NotContains(t, response.Body.String(), "matrix_receipts")
			require.NotContains(t, response.Body.String(), "SELECT")
			require.Contains(t, response.Body.String(), "unavailable")
		})
	}
}
