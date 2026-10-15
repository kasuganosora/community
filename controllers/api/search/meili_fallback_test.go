package search

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kasuganosora/community/models"
	searchpkg "github.com/kasuganosora/community/search"
	"github.com/stretchr/testify/require"
)

// fakeMeili answers every index search with status (200 = empty hits) and
// keeps /version healthy so a failed search does not disable the client.
func fakeMeili(t *testing.T, status int) *atomic.Int32 {
	t.Helper()
	var searches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/search"):
			searches.Add(1)
			w.WriteHeader(status)
			if status == 200 {
				_, _ = w.Write([]byte(`{"hits":[],"estimatedTotalHits":0}`))
			} else {
				_, _ = w.Write([]byte(`{"message":"boom","code":"internal","type":"internal"}`))
			}
		case r.URL.Path == "/version":
			_, _ = w.Write([]byte(`{"pkgVersion":"1.0.0"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(searchpkg.UseMeilisearchForTest(srv.URL))
	return &searches
}

func meiliFallbackFixture(t *testing.T) *models.Account {
	t.Helper()
	conn := sqlTestDB(t)
	require.NoError(t, conn.AutoMigrate(&models.MuteAccount{}, &models.MuteStatus{}, &models.PinStatus{}, &models.FavoriteStatus{}, &models.BookmarkStatus{}))
	viewer := &models.Account{Username: "tentacle"}
	require.NoError(t, conn.Create(viewer).Error)
	require.NoError(t, conn.Create(&models.Account{Username: "fan", DisplayName: "tentacle fan"}).Error)
	st := &models.Status{AccountId: viewer.Id, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, conn.Create(st).Error)
	require.NoError(t, conn.Create(&models.StatusContent{StatusId: st.Id, Content: "<p>tentacle here</p>"}).Error)
	require.NoError(t, conn.Create(&models.Tag{Name: "tentacle", NameKey: "tentacle"}).Error)
	return viewer
}

func TestSearchEmptyMeiliResultIsFinal(t *testing.T) {
	viewer := meiliFallbackFixture(t)
	searches := fakeMeili(t, 200)
	// "tentacle fan" is not an acct handle, so accounts go through Meili too.
	for _, typ := range []string{"statuses", "accounts", "hashtags"} {
		r := runSearch(t, "type="+typ+"&q=tentacle%20fan&offset=10&limit=10", viewer)
		require.Empty(t, r.Statuses, typ)
		require.Empty(t, r.Accounts, typ)
		require.Empty(t, r.Hashtags, typ)
	}
	require.EqualValues(t, 3, searches.Load())
}

func TestSearchFallsBackToSQLWhenMeiliErrors(t *testing.T) {
	viewer := meiliFallbackFixture(t)
	searches := fakeMeili(t, 500)
	require.Len(t, runSearch(t, "type=statuses&q=tentacle%20here", viewer).Statuses, 1)
	require.Len(t, runSearch(t, "type=accounts&q=tentacle%20fan", viewer).Accounts, 1)
	require.Len(t, runSearch(t, "type=hashtags&q=%23tentacle", viewer).Hashtags, 1)
	require.EqualValues(t, 3, searches.Load())
}
