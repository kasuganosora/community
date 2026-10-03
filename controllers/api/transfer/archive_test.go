package transfer

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	data "github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"net/http/httptest"
	"testing"
)

func archiveTestRouter(account *models.Account) *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if account != nil {
			c.Set("account", account)
		}
	})
	r.GET("/archive", ArchivedPosts)
	r.GET("/archive/:id", ArchivedPost)
	return r
}
func archiveGet(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	return w
}
func archiveSeed(t *testing.T, db *gorm.DB, id, user, account uint64, kind string, p any) {
	t.Helper()
	b, e := json.Marshal(p)
	require.NoError(t, e)
	require.NoError(t, db.Create(&data.StoredPreservedRecord{ID: id, Namespace: "ns", Scope: "personal", OwnerUserID: user, OwnerAccountID: account, Kind: kind, SourceID: "source", Data: b}).Error)
}
func TestPrivateArchiveOwnerKindSanitizationAndPagination(t *testing.T) {
	db := transferDB(t)
	require.NoError(t, db.AutoMigrate(&data.StoredPreservedRecord{}, &data.ImportReceipt{}, &models.MediaAttachment{}))
	p := data.Post{ID: "p", AuthorID: "remote-author", Text: "private message", HTML: `<p>safe<script>secret()</script><a href="javascript:evil()">link</a></p>`, SpoilerText: "<b>cw</b>", URI: "javascript:evil()", Extensions: json.RawMessage(`{"private_key":"NEVER_RETURN"}`)}
	archiveSeed(t, db, 1, 1, 10, "post", p)
	archiveSeed(t, db, 2, 1, 10, "post", p)
	archiveSeed(t, db, 3, 1, 11, "post", p)
	archiveSeed(t, db, 4, 2, 10, "post", p)
	archiveSeed(t, db, 5, 1, 10, "user_profile", map[string]string{"private_key": "NEVER_RETURN"})
	archiveSeed(t, db, 6, 0, 0, "post", p)
	archiveSeed(t, db, 7, 1, 10, "post", map[string]string{"private_key": "NEVER_RETURN"})
	archiveSeed(t, db, 8, 1, 10, "post", p)
	require.NoError(t, db.Model(&data.StoredPreservedRecord{}).Where("id = ?", 8).Update("scope", "site").Error)
	archiveSeed(t, db, 9, 1, 10, "post", p)
	require.NoError(t, db.Model(&data.StoredPreservedRecord{}).Where("id = ?", 9).Update("scope", "").Error)
	r := archiveTestRouter(&models.Account{Id: 10, UserId: 1})
	w := archiveGet(r, "/archive?limit=2")
	require.Equal(t, 200, w.Code)
	require.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
	require.NotContains(t, w.Body.String(), "NEVER_RETURN")
	require.NotContains(t, w.Body.String(), "javascript:")
	require.NotContains(t, w.Body.String(), "script>")
	var response struct {
		Items []archivedPost `json:"items"`
		Next  string         `json:"next_max_id"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Len(t, response.Items, 1)
	require.Equal(t, "2", response.Items[0].ID)
	require.True(t, response.Items[0].PrivateArchive)
	require.Equal(t, "cw", response.Items[0].Spoiler)
	require.Equal(t, "2", response.Next)
	w = archiveGet(r, "/archive?max_id=2")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"id":"1"`)
	require.NotContains(t, w.Body.String(), `"id":"2"`)
	for _, id := range []string{"3", "4", "5", "6", "7", "8", "9", "0", "invalid"} {
		require.Equal(t, 404, archiveGet(r, "/archive/"+id).Code)
	}
	require.Equal(t, 401, archiveGet(archiveTestRouter(nil), "/archive").Code)
	for _, query := range []string{"limit=51", "limit=0", "max_id=-1", "since_id=x"} {
		require.Equal(t, 422, archiveGet(r, "/archive?"+query).Code)
	}
}
func TestPrivateArchiveMediaNamespaceAndOwnerChecks(t *testing.T) {
	db := transferDB(t)
	require.NoError(t, db.AutoMigrate(&data.StoredPreservedRecord{}, &data.ImportReceipt{}, &models.MediaAttachment{}))
	archiveSeed(t, db, 1, 1, 10, "post", data.Post{ID: "p", AuthorID: "remote", MediaIDs: []string{"own", "foreign", "missing", "othernamespace"}})
	for _, v := range []models.MediaAttachment{{Id: 100, AccountId: 10, FileName: "safe.txt", FilePath: "PRIVATE_PATH", ContentType: "text/plain"}, {Id: 101, AccountId: 20, FileName: "OTHER_SECRET", FilePath: "OTHER_PATH"}} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&v).Error)
	}
	for _, v := range []data.ImportReceipt{{Namespace: "ns", Kind: "media", SourceHash: "one", SourceID: "own", TargetID: 100}, {Namespace: "ns", Kind: "media", SourceHash: "two", SourceID: "foreign", TargetID: 101}, {Namespace: "other", Kind: "media", SourceHash: "three", SourceID: "othernamespace", TargetID: 100}} {
		require.NoError(t, db.Create(&v).Error)
	}
	w := archiveGet(archiveTestRouter(&models.Account{Id: 10, UserId: 1}), "/archive/1")
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "PRIVATE_PATH")
	require.NotContains(t, w.Body.String(), "OTHER_SECRET")
	var p archivedPost
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p))
	require.Len(t, p.Media, 4)
	require.Equal(t, "100", p.Media[0].ID)
	require.True(t, p.Media[0].Available)
	for _, m := range p.Media[1:] {
		require.False(t, m.Available)
		require.Empty(t, m.ID)
	}
}
