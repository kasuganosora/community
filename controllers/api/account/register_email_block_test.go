package account

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRegisterRejectsBlockedEmailDomains(t *testing.T) {
	db, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&models.EmailDomainBlock{}))
	require.NoError(t, db.Create(&models.EmailDomainBlock{Domain: "spam.example"}).Error)
	database.SetTestDB(db)
	t.Cleanup(database.ResetTestDB)

	post := func(email string) *httptest.ResponseRecorder {
		form := url.Values{"user[account_attributes][username]": {"alice"}, "user[account_attributes][display_name]": {"Alice"}, "user[email]": {email}, "user[password]": {"Passw0rd!123"},
			"user[password_confirmation]": {"Passw0rd!123"}}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/api/v1/accounts/register", strings.NewReader(form.Encode()))
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		Register(c)
		return w
	}
	for _, email := range []string{"a@spam.example", "a@SPAM.Example", "a@mail.sub.spam.example"} {
		w := post(email)
		assert.Equal(t, 422, w.Code, email)
		assert.Contains(t, w.Body.String(), "E-mail provider is not allowed", email)
		assert.Contains(t, w.Body.String(), "ERR_BLOCKED")
	}
	// Other domains are not blocked by this check (here: registration is closed).
	assert.NotEqual(t, 422, post("a@notspam.example").Code)
}
