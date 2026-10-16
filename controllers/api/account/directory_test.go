package account

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupDirectoryDB(t *testing.T) *gorm.DB {
	t.Helper()

	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(
		&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.Emoji{},
		&models.Block{}, &models.MuteAccount{}, &models.DomainBlock{},
	))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)

	return conn
}

type directoryFixture struct {
	alice *models.Account
}

func seedDirectory(t *testing.T, conn *gorm.DB) directoryFixture {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Second)
	ago := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
	domain := func(s string) *string { return &s }

	type spec struct {
		username     string
		domain       *string
		discoverable bool
		locked       bool
		accountBan   bool
		userStatus   int
		userBan      bool
		lastStatus   *time.Time
		created      time.Duration
	}

	specs := []spec{
		{username: "alice", discoverable: true, lastStatus: ago(time.Hour), created: 50 * time.Hour},
		{username: "bob", discoverable: true, lastStatus: ago(10 * time.Minute), created: 40 * time.Hour},
		{username: "carol", discoverable: false, lastStatus: ago(time.Minute), created: 30 * time.Hour},
		{username: "dave", discoverable: true, accountBan: true, lastStatus: ago(time.Minute), created: 30 * time.Hour},
		{username: "erin", discoverable: true, userStatus: models.UserStatusUnapproved, lastStatus: ago(time.Minute), created: 30 * time.Hour},
		{username: "frank", discoverable: true, userBan: true, lastStatus: ago(time.Minute), created: 30 * time.Hour},
		{username: "gina", domain: domain("remote.example"), discoverable: true, created: 5 * time.Hour},
		{username: "hank", domain: domain("Bad.Example"), discoverable: true, lastStatus: ago(time.Minute), created: 30 * time.Hour},
		{username: "ivy", domain: domain("quiet.example"), discoverable: true, lastStatus: ago(time.Minute), created: 30 * time.Hour},
		{username: "lena", discoverable: true, locked: true, lastStatus: ago(2 * time.Hour), created: 20 * time.Hour},
		{username: "mike", discoverable: true, lastStatus: ago(3 * time.Hour), created: 10 * time.Hour},
	}

	byName := map[string]*models.Account{}

	for i, s := range specs {
		// Explicit IDs: SQLite does not auto-increment a bigint(20) key.
		account := &models.Account{
			Id:           uint64(i + 1),
			Username:     s.username,
			Domain:       s.domain,
			DisplayName:  s.username,
			Discoverable: s.discoverable,
			Locked:       s.locked,
			IsBan:        s.accountBan,
			LastStatusAt: s.lastStatus,
			CreatedAt:    ago(s.created),
		}

		if s.domain == nil {
			user := &models.User{
				UserName:   s.username,
				EmailHash:  s.username + "-hash",
				Status:     s.userStatus,
				IsBan:      s.userBan,
				Properties: map[string]any{},
			}
			require.NoError(t, conn.Create(user).Error)
			account.UserId = user.Id
		}

		require.NoError(t, conn.Create(account).Error)
		byName[s.username] = account
	}

	require.NoError(t, conn.Create(&models.DomainBlock{Domain: "bad.example", Severity: "suspend"}).Error)
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: "quiet.example", Severity: "silence"}).Error)

	alice := byName["alice"]
	require.NoError(t, conn.Create(&models.Block{AccountId: alice.Id, Type: models.BlockTypeAccount, TargetAccountId: byName["bob"].Id}).Error)
	require.NoError(t, conn.Create(&models.Block{AccountId: byName["mike"].Id, Type: models.BlockTypeAccount, TargetAccountId: alice.Id}).Error)
	require.NoError(t, conn.Create(&models.Block{AccountId: alice.Id, Type: models.BlockTypeDomain, TargetDomain: "REMOTE.example"}).Error)
	require.NoError(t, conn.Create(&models.MuteAccount{AccountId: alice.Id, TargetId: byName["lena"].Id, ExpiredAt: ago(time.Minute)}).Error)

	return directoryFixture{alice: alice}
}

func requestDirectory(t *testing.T, query string, viewer *models.Account) []string {
	t.Helper()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/api/v1/directory"+query, nil)

	if viewer != nil {
		c.Set("account", viewer)
	}

	GetDirectory(c)
	require.Empty(t, c.Errors)
	require.Equal(t, 200, w.Code, w.Body.String())

	var rows []struct {
		Acct string `json:"acct"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &rows))

	accts := make([]string, 0, len(rows))
	for _, row := range rows {
		accts = append(accts, row.Acct)
	}

	return accts
}

func TestGetDirectory(t *testing.T) {
	conn := setupDirectoryDB(t)
	fixture := seedDirectory(t, conn)

	t.Run("active order excludes hidden accounts", func(t *testing.T) {
		require.Equal(t,
			[]string{"bob", "alice", "lena", "mike", "gina@remote.example"},
			requestDirectory(t, "", nil))
		require.Equal(t,
			[]string{"bob", "alice", "lena", "mike", "gina@remote.example"},
			requestDirectory(t, "?order=active", nil))
	})

	t.Run("new order", func(t *testing.T) {
		require.Equal(t,
			[]string{"gina@remote.example", "mike", "lena", "bob", "alice"},
			requestDirectory(t, "?order=new", nil))
	})

	t.Run("local only", func(t *testing.T) {
		require.Equal(t,
			[]string{"bob", "alice", "lena", "mike"},
			requestDirectory(t, "?local=true", nil))
		require.Equal(t,
			[]string{"mike", "lena", "bob", "alice"},
			requestDirectory(t, "?local=true&order=new", nil))
	})

	t.Run("offset and limit", func(t *testing.T) {
		require.Equal(t, []string{"bob", "alice"}, requestDirectory(t, "?limit=2", nil))
		require.Equal(t, []string{"lena", "mike"}, requestDirectory(t, "?limit=2&offset=2", nil))
		require.Equal(t, []string{"gina@remote.example"}, requestDirectory(t, "?limit=2&offset=4", nil))
		require.Empty(t, requestDirectory(t, "?offset=10", nil))
		require.Len(t, requestDirectory(t, "?limit=1000", nil), 5)
	})

	t.Run("viewer exclusions", func(t *testing.T) {
		// bob is blocked by alice, mike blocks alice, gina's domain is
		// blocked by alice; the expired mute on lena no longer applies.
		require.Equal(t, []string{"alice", "lena"}, requestDirectory(t, "", fixture.alice))
	})
}

func TestGetDirectoryHidesBlockedSubdomains(t *testing.T) {
	conn := setupDirectoryDB(t)
	mk := func(id uint64, user, domain string) {
		d := domain
		require.NoError(t, conn.Create(&models.Account{Id: id, Username: user, Domain: &d, DisplayName: user, Discoverable: true}).Error)
	}
	mk(1, "sub", "a.Bad.Example")
	mk(2, "deep", "x.y.bad.example")
	mk(3, "lookalike", "notbad.example")
	mk(4, "wild", "a_b.example")
	mk(5, "ok", "good.example")
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: "bad.example", Severity: "suspend"}).Error)
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: "a_b.example", Severity: "silence"}).Error)

	got := requestDirectory(t, "?order=new", nil)
	require.ElementsMatch(t, []string{"lookalike@notbad.example", "ok@good.example"}, got)
}
