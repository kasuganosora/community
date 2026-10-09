package commands

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type fakeActorFetcher struct {
	mu      sync.Mutex
	actors  map[string]*activitypub.Actor
	errs    map[string]error
	objects map[string]map[string]any
	fetched []string
}

func (f *fakeActorFetcher) FetchActor(_ context.Context, uri string) (*activitypub.Actor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetched = append(f.fetched, uri)
	if err := f.errs[uri]; err != nil {
		return nil, err
	}
	if actor, ok := f.actors[uri]; ok {
		copied := *actor
		return &copied, nil
	}
	return nil, &activitypub.FetchError{StatusCode: 404}
}

func (f *fakeActorFetcher) FetchObject(_ context.Context, uri string) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if obj, ok := f.objects[uri]; ok {
		return obj, nil
	}
	return nil, &activitypub.FetchError{StatusCode: 404}
}

func remoteAccount(t *testing.T, conn *gorm.DB, id uint64, username, domain string, banned bool) *models.Account {
	t.Helper()
	d := domain
	account := &models.Account{Id: id, Username: username, Domain: &d, OriginId: "https://" + domain + "/users/" + username,
		DisplayName: "old " + username, IsBan: banned}
	require.NoError(t, conn.Create(account).Error)
	return account
}

func setupRefreshTest(t *testing.T) (*gorm.DB, *fakeActorFetcher) {
	t.Helper()
	conn := setupAccountsTest(t)
	require.NoError(t, conn.AutoMigrate(&models.AccountEmoji{}, &models.DomainBlock{}))

	remoteAccount(t, conn, 1, "alice", "remote.example", false)
	remoteAccount(t, conn, 2, "bob", "remote.example", true)    // banned
	remoteAccount(t, conn, 3, "carol", "gone.example", false)   // 410
	remoteAccount(t, conn, 4, "dave", "broken.example", false)  // 500
	remoteAccount(t, conn, 5, "erin", "blocked.example", false) // domain suspended
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: "blocked.example", Severity: "suspend"}).Error)
	require.NoError(t, conn.Create(&models.Account{Id: 6, Username: "local", DisplayName: "local"}).Error)

	alice := "https://remote.example/users/alice"
	fetcher := &fakeActorFetcher{
		actors: map[string]*activitypub.Actor{alice: {
			ID: alice, Type: "Person", PreferredUsername: "alice", Name: "Alice :wave:",
			Summary:   `<p>bio<script>x</script></p>`,
			Followers: alice + "/followers", Following: alice + "/following", Outbox: alice + "/outbox",
			Icon:                      &activitypub.Image{URL: "https://remote.example/a.png"},
			Image:                     &activitypub.Image{URL: "https://remote.example/h.png"},
			Attachments:               []activitypub.Attachment{{Type: "PropertyValue", Name: "Site", Value: `<a href="https://a.example">a.example</a>`}},
			ManuallyApprovesFollowers: true,
		}},
		errs: map[string]error{
			"https://gone.example/users/carol":  &activitypub.FetchError{StatusCode: 410},
			"https://broken.example/users/dave": fmt.Errorf("boom: %w", &activitypub.FetchError{StatusCode: 500}),
		},
		objects: map[string]map[string]any{
			alice + "/followers": {"totalItems": float64(120)},
			alice + "/following": {"totalItems": float64(7)},
			alice + "/outbox":    {"totalItems": float64(3456)},
		},
	}
	prev := newRefreshFetcher
	newRefreshFetcher = func() activitypub.ActorFetcher { return fetcher }
	t.Cleanup(func() { newRefreshFetcher = prev })
	return conn, fetcher
}

func loadAccount(t *testing.T, conn *gorm.DB, username string) (models.Account, models.AccountExtra) {
	t.Helper()
	var account models.Account
	require.NoError(t, conn.Where("username = ?", username).First(&account).Error)
	var extra models.AccountExtra
	require.NoError(t, conn.Where("account_id = ?", account.Id).First(&extra).Error)
	return account, extra
}

func TestAccountsRefreshSingleAccount(t *testing.T) {
	conn, fetcher := setupRefreshTest(t)

	res := runAccounts("", "refresh", "@Alice@Remote.example", "--dry-run")
	require.NoError(t, res.err, res.out)
	assert.Contains(t, res.out, "ok    alice@remote.example")
	assert.Contains(t, res.out, "dry run")
	account, extra := loadAccount(t, conn, "alice")
	assert.Equal(t, "old alice", account.DisplayName, "dry run changes nothing")
	assert.Zero(t, extra.FollowersCount)

	res = runAccounts("", "refresh", "alice@remote.example")
	require.NoError(t, res.err, res.out)
	assert.Contains(t, res.out, "Refreshed 1 of 1 accounts")
	account, extra = loadAccount(t, conn, "alice")
	assert.Equal(t, "Alice :wave:", account.DisplayName)
	assert.NotContains(t, account.Note, "script")
	assert.Equal(t, "https://remote.example/a.png", account.Avatar)
	assert.Equal(t, "https://remote.example/h.png", account.Header)
	assert.True(t, account.Locked)
	assert.Equal(t, 120, extra.FollowersCount)
	assert.Equal(t, 7, extra.FollowingCount)
	assert.Equal(t, 3456, extra.StatusesCount)
	require.Len(t, extra.Fields, 1)
	assert.Equal(t, "Site", extra.Fields[0].Name)
	assert.Contains(t, extra.Fields[0].Val, "a.example")
	assert.Len(t, fetcher.fetched, 2)

	res = runAccounts("", "refresh", "nobody@remote.example")
	assert.Error(t, res.err, "an unknown account is a failure")
	assert.Contains(t, res.out, "no such account")
	res = runAccounts("", "refresh")
	assert.Error(t, res.err)
	res = runAccounts("", "refresh", "--all", "--domain", "remote.example")
	assert.Error(t, res.err, "modes are exclusive")
}

func TestAccountsRefreshAllAndDomain(t *testing.T) {
	conn, fetcher := setupRefreshTest(t)

	res := runAccounts("", "refresh", "--all", "-c", "2", "--rate", "0")
	require.NoError(t, res.err, res.out)
	assert.Contains(t, res.out, "Refreshed 1 of 5 accounts: 1 gone, 2 skipped, 1 failed", res.out)
	assert.Contains(t, res.out, "gone  carol@gone.example")
	assert.Contains(t, res.out, "skip  bob@remote.example: suspended")
	assert.Contains(t, res.out, "skip  erin@blocked.example: domain suspended")
	assert.Contains(t, res.out, "fail  dave@broken.example")
	for _, uri := range fetcher.fetched {
		assert.False(t, strings.Contains(uri, "bob") || strings.Contains(uri, "erin"), "skipped accounts are not fetched: %s", uri)
	}
	_, extra := loadAccount(t, conn, "alice")
	assert.Equal(t, 120, extra.FollowersCount)

	fetcher.fetched = nil
	res = runAccounts("", "refresh", "--domain", "GONE.example")
	require.NoError(t, res.err, res.out)
	assert.Equal(t, []string{"https://gone.example/users/carol"}, fetcher.fetched)
	assert.Contains(t, res.out, "Refreshed 0 of 1 accounts: 1 gone")
}

func TestNormalizeRefreshArgs(t *testing.T) {
	got := NormalizeAccountArgs([]string{"tootctl", "accounts", "refresh", "a@b.example", "--dry-run", "-c", "2", "c@d.example", "--rate=1"})
	assert.Equal(t, []string{"tootctl", "accounts", "refresh", "--dry-run", "-c", "2", "--rate=1", "a@b.example", "c@d.example"}, got)
}
