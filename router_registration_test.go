package community

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSetRouterRegistersWithoutPanic builds the full route tree. gin panics at
// registration time on duplicate or conflicting method+path pairs, which would
// crash the server at startup.
func TestSetRouterRegistersWithoutPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)

	testDB, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	database.SetTestDB(testDB.GormDB)

	app := gin.New()

	require.NotPanics(t, func() { SetRouter(app) })

	seen := map[string]bool{}

	for _, r := range app.Routes() {
		key := r.Method + " " + r.Path
		assert.False(t, seen[key], "duplicate route %s", key)
		seen[key] = true
	}

	assert.NotEmpty(t, seen)
	assert.True(t, seen["GET /api/v2/filters"])
	assert.True(t, seen["GET /api/v1/filters"])
	assert.True(t, seen["GET /api/v1/timelines/public"])
}
