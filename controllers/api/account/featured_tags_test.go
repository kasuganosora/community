package account

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func featuredTagsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&models.Account{}, &models.AccountExtra{}, &models.Tag{}, &models.StatusTag{}, &FeaturedTag{}))
	require.NoError(t, db.Session(&gorm.Session{}).Exec(`CREATE TABLE statuses (id BIGINT PRIMARY KEY, account_id BIGINT NOT NULL,
		visibility SMALLINT NOT NULL DEFAULT 0, created_at TIMESTAMP NOT NULL, deleted_at TIMESTAMP NULL)`).Error)
	database.SetTestDB(db)
	t.Cleanup(database.ResetTestDB)
	return db
}

type featuredCall struct {
	code int
	body []byte
	errs int
}

func callFeatured(t *testing.T, handler gin.HandlerFunc, actor *models.Account, method string, params gin.Params, body any) featuredCall {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	c.Request = httptest.NewRequest(method, "/", bytes.NewReader(payload))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Host = "c.example"
	c.Params = params
	if actor != nil {
		c.Set("account", actor)
	}
	handler(c)
	return featuredCall{code: w.Code, body: w.Body.Bytes(), errs: len(c.Errors)}
}

func TestFeaturedTagsCRUD(t *testing.T) {
	db := featuredTagsDB(t)
	alice := &models.Account{Id: 1, Username: "alice"}
	require.NoError(t, db.Create(alice).Error)

	// Create, keeping the case-insensitive tag identity.
	res := callFeatured(t, CreateFeaturedTag, alice, "POST", nil, gin.H{"name": "#OSINT"})
	require.Equal(t, 200, res.code, string(res.body))
	var created map[string]any
	require.NoError(t, json.Unmarshal(res.body, &created))
	assert.Equal(t, "OSINT", created["name"])
	assert.EqualValues(t, 0, created["statuses_count"])
	assert.Equal(t, "https://c.example/@alice/tagged/OSINT", created["url"])

	res = callFeatured(t, CreateFeaturedTag, alice, "POST", nil, gin.H{"name": "osint"})
	assert.Equal(t, 422, res.code, "a case variant is the same featured tag")
	res = callFeatured(t, CreateFeaturedTag, alice, "POST", nil, gin.H{"name": "<b>x"})
	assert.Equal(t, 422, res.code)

	// Two public statuses and one direct one use the tag.
	tag, err := models.FindTagByName(db, "osint")
	require.NoError(t, err)
	day := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for id, vis := range map[uint64]int{100: models.StatusVisibilityPublic, 101: models.StatusVisibilityUnlisted, 102: models.StatusVisibilityDirect} {
		require.NoError(t, db.Exec("INSERT INTO statuses (id, account_id, visibility, created_at) VALUES (?, ?, ?, ?)", id, alice.Id, vis, day.Add(time.Duration(id)*time.Hour)).Error)
		require.NoError(t, db.Create(&models.StatusTag{StatusId: id, TagId: tag.Id}).Error)
	}

	var listed []map[string]any
	res = callFeatured(t, ListFeaturedTags, alice, "GET", nil, nil)
	require.Equal(t, 200, res.code, string(res.body))
	require.NoError(t, json.Unmarshal(res.body, &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, created["id"], listed[0]["id"])
	assert.EqualValues(t, 2, listed[0]["statuses_count"], "direct statuses are not counted")
	assert.Equal(t, "2026-10-05", listed[0]["last_status_at"])

	// Public view of the account.
	res = callFeatured(t, AccountFeaturedTags, nil, "GET", gin.Params{{Key: "id", Value: "1"}}, nil)
	require.Equal(t, 200, res.code, string(res.body))
	require.NoError(t, json.Unmarshal(res.body, &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, "OSINT", listed[0]["name"])
	assert.Equal(t, 404, callFeatured(t, AccountFeaturedTags, nil, "GET", gin.Params{{Key: "id", Value: "999"}}, nil).code)
	assert.Equal(t, 400, callFeatured(t, AccountFeaturedTags, nil, "GET", gin.Params{{Key: "id", Value: "x"}}, nil).code)

	// Delete: someone else's id is not found; then own delete works once.
	bob := &models.Account{Id: 2, Username: "bob"}
	require.NoError(t, db.Create(bob).Error)
	id := created["id"].(string)
	assert.Equal(t, 404, callFeatured(t, DeleteFeaturedTag, bob, "DELETE", gin.Params{{Key: "id", Value: id}}, nil).code)
	assert.Equal(t, 200, callFeatured(t, DeleteFeaturedTag, alice, "DELETE", gin.Params{{Key: "id", Value: id}}, nil).code)
	assert.Equal(t, 404, callFeatured(t, DeleteFeaturedTag, alice, "DELETE", gin.Params{{Key: "id", Value: id}}, nil).code)

	res = callFeatured(t, ListFeaturedTags, alice, "GET", nil, nil)
	require.Equal(t, 200, res.code)
	assert.JSONEq(t, "[]", string(res.body))
}

// A database error (here: the table missing, as on the deployed server) is
// a logged 500, not an empty list.
func TestFeaturedTagsReportDatabaseErrors(t *testing.T) {
	db := featuredTagsDB(t)
	alice := &models.Account{Id: 1, Username: "alice"}
	require.NoError(t, db.Create(alice).Error)
	require.NoError(t, db.Migrator().DropTable(&FeaturedTag{}))

	for _, res := range []featuredCall{
		callFeatured(t, AccountFeaturedTags, nil, "GET", gin.Params{{Key: "id", Value: "1"}}, nil),
		callFeatured(t, ListFeaturedTags, alice, "GET", nil, nil),
		callFeatured(t, CreateFeaturedTag, alice, "POST", nil, gin.H{"name": "osint"}),
		callFeatured(t, DeleteFeaturedTag, alice, "DELETE", gin.Params{{Key: "id", Value: "1"}}, nil),
		callFeatured(t, SuggestFeaturedTags, alice, "GET", nil, nil),
	} {
		assert.Equal(t, 500, res.code, string(res.body))
		assert.Equal(t, 1, res.errs, "the error is attached to the request for logging")
	}
}
