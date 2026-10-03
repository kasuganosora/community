package search

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestAnonymousResolveSearchStaysLocal(t *testing.T) {
	conn := testutil.QuickDBSetup(t, &models.Account{}, &models.AccountExtra{}, &models.Emoji{})
	database.SetTestDB(conn.GormDB)
	t.Cleanup(database.ResetTestDB)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v2/search?type=accounts&q=http%3A%2F%2F127.0.0.1%3A1%2Factor&resolve=true", nil)
	Search(c)
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{"accounts":[],"statuses":[],"hashtags":[]}`, w.Body.String())
}
