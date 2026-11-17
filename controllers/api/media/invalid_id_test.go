package media

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMediaHandlersRejectNonNumericIDsBeforeSQL(t *testing.T) {
	gdb := setupMediaTestDB(t)
	gin.SetMode(gin.TestMode)

	var sqlQueries atomic.Int64
	require.NoError(t, gdb.Callback().Query().Before("gorm:query").Register("test:forbid_invalid_media_id_sql", func(*gorm.DB) {
		sqlQueries.Add(1)
	}))

	// Close the underlying SQL pool as an extra guarantee: any handler that
	// reaches SQL for Get/GetProcessing/Update/Delete will error instead of 404.
	sqlDB, err := gdb.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	invalidIDs := []string{
		"system",
		"abc",
		"-1",
		"0",
		"12.34",
		"99999999999999999999999999",
	}

	t.Run("Serve_never_queries_SQL_for_system_or_non_numeric_id", func(t *testing.T) {
		r := gin.New()
		r.GET("/media/:id/*file", Serve)

		for _, badID := range invalidIDs {
			sqlQueries.Store(0)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/media/"+badID+"/media_attachments/files/a.png", nil)
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusNotFound, w.Code, "id=%q", badID)
			require.Zero(t, sqlQueries.Load(), "Serve must reject non-numeric id=%q before SQL", badID)
		}
	})

	t.Run("Get_GetProcessing_Update_Delete_reject_non_numeric_id_before_SQL", func(t *testing.T) {
		for _, badID := range invalidIDs {
			for _, tc := range []struct {
				name   string
				method string
				body   []byte
				fn     gin.HandlerFunc
			}{
				{name: "Get", method: http.MethodGet, fn: Get},
				{name: "GetProcessing", method: http.MethodGet, fn: GetProcessing},
				{name: "Update", method: http.MethodPut, body: []byte(`{"description":"x"}`), fn: Update},
				{name: "Delete", method: http.MethodDelete, fn: Delete},
			} {
				sqlQueries.Store(0)
				req := httptest.NewRequest(tc.method, "/api/v1/media/"+badID, bytes.NewReader(tc.body))
				if len(tc.body) > 0 {
					req.Header.Set("Content-Type", "application/json")
				}
				c, w := newHandlerContext(t, gin.Params{{Key: "id", Value: badID}}, req)
				tc.fn(c)
				require.Equal(t, http.StatusNotFound, w.Code, "%s id=%q", tc.name, badID)
				require.Empty(t, c.Errors, "%s id=%q must not push SQL error", tc.name, badID)
				require.Zero(t, sqlQueries.Load(), "%s must reject id=%q before SQL", tc.name, badID)
			}
		}
	})
}
