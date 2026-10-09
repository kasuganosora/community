package activitypub

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// misskeyActor mirrors https://maid.lat/users/9h6zahxdlu (Misskey 2024+).
const misskeyActor = `{"@context":["https://www.w3.org/ns/activitystreams","https://w3id.org/security/v1",{"misskey":"https://misskey-hub.net/ns#","_misskey_summary":"misskey:_misskey_summary","isCat":"misskey:isCat"}],
"type":"Person","id":"https://maid.example/users/9h6zahxdlu","inbox":"https://maid.example/users/9h6zahxdlu/inbox","outbox":"https://maid.example/users/9h6zahxdlu/outbox",
"followers":"https://maid.example/users/9h6zahxdlu/followers","following":"https://maid.example/users/9h6zahxdlu/following","featured":"https://maid.example/users/9h6zahxdlu/collections/featured",
"sharedInbox":"https://maid.example/inbox","endpoints":{"sharedInbox":"https://maid.example/inbox"},"url":"https://maid.example/@luna","preferredUsername":"luna","name":"露娜",
"summary":"<p>maid</p>","_misskey_summary":"maid","_misskey_followedMessage":"meow","_misskey_requireSigninToViewContents":false,"_misskey_makeNotesFollowersOnlyBefore":null,
"icon":{"type":"Image","url":"https://files.maid.example/a.webp","sensitive":false,"name":null},"image":{"type":"Image","url":"https://files.maid.example/h.jpg","sensitive":false,"name":null},
"tag":[],"manuallyApprovesFollowers":false,"discoverable":true,"isCat":true,"alsoKnownAs":["https://old.example/users/luna"],
"publicKey":{"id":"https://maid.example/users/9h6zahxdlu#main-key","type":"Key","owner":"https://maid.example/users/9h6zahxdlu","publicKeyPem":"x"},"attachment":[]}`

const misskeyNote = `{"@context":"https://www.w3.org/ns/activitystreams","id":"https://maid.example/notes/a1b2c3","type":"Note","attributedTo":"https://maid.example/users/9h6zahxdlu",
"content":"<p>hello from misskey</p>","_misskey_content":"hello from misskey","published":"2026-10-08T12:00:00.000Z","to":["https://www.w3.org/ns/activitystreams#Public"],
"cc":["https://maid.example/users/9h6zahxdlu/followers"],"inReplyTo":null,"attachment":[],"sensitive":false,"tag":[]}`

// mastodonActor uses Mastodon's shapes (alsoKnownAs array, url string,
// attachment PropertyValues).
const mastodonActor = `{"@context":["https://www.w3.org/ns/activitystreams"],"id":"https://masto.example/users/Gargron","type":"Person","preferredUsername":"Gargron","name":"Eugen",
"inbox":"https://masto.example/users/Gargron/inbox","url":"https://masto.example/@Gargron","alsoKnownAs":[],"published":"2016-03-16T00:00:00Z",
"icon":{"type":"Image","mediaType":"image/png","url":"https://files.masto.example/a.png"},"attachment":[{"type":"PropertyValue","name":"Patreon","value":"x"}],
"publicKey":{"id":"https://masto.example/users/Gargron#main-key","owner":"https://masto.example/users/Gargron","publicKeyPem":"x"}}`

const mastodonNote = `{"id":"https://masto.example/users/Gargron/statuses/123","type":"Note","attributedTo":"https://masto.example/users/Gargron","url":"https://masto.example/@Gargron/123",
"content":"<p>toot</p>","published":"2026-10-08T12:00:00Z","to":["https://www.w3.org/ns/activitystreams#Public"],"cc":[],"tag":[],"attachment":[]}`

type fakeFediverse struct {
	unsigned atomic.Int32
	signed   atomic.Int32
	key      *rsa.PrivateKey
}

// serve emulates maid.example (Misskey, open fetch) and masto.example
// (Mastodon with authorized fetch: every ActivityPub GET must be signed).
func (f *fakeFediverse) serve(t *testing.T) func(*http.Request) (*http.Response, error) {
	return func(r *http.Request) (*http.Response, error) {
		key := r.URL.Host + r.URL.Path
		if r.URL.Path == "/.well-known/webfinger" {
			switch r.URL.Query().Get("resource") {
			case "acct:luna@maid.example":
				return outboundResponse(200, `{"subject":"acct:luna@maid.example","links":[{"rel":"self","type":"application/activity+json","href":"https://maid.example/users/9h6zahxdlu"}]}`), nil
			case "acct:Gargron@masto.example", "acct:gargron@masto.example":
				return outboundResponse(200, `{"subject":"acct:Gargron@masto.example","links":[{"rel":"self","type":"application/ld+json; profile=\"https://www.w3.org/ns/activitystreams\"","href":"https://masto.example/users/Gargron"}]}`), nil
			}
			return outboundResponse(404, ""), nil
		}
		require.Contains(t, r.Header.Get("Accept"), "application/activity+json")
		if strings.HasPrefix(key, "masto.example/") {
			if r.Header.Get("Signature") == "" {
				f.unsigned.Add(1)
				return outboundResponse(401, `{"error":"Request not signed"}`), nil
			}
			require.NoError(t, VerifyRequest(r, &f.key.PublicKey))
			require.Contains(t, r.Header.Get("Signature"), `headers="(request-target) host date"`)
			f.signed.Add(1)
		}
		switch key {
		case "maid.example/users/9h6zahxdlu", "maid.example/@luna":
			return outboundResponse(200, misskeyActor), nil
		case "maid.example/notes/a1b2c3":
			return outboundResponse(200, misskeyNote), nil
		case "masto.example/users/Gargron", "masto.example/@Gargron":
			return outboundResponse(200, mastodonActor), nil
		case "masto.example/users/Gargron/statuses/123", "masto.example/@Gargron/123":
			return outboundResponse(200, mastodonNote), nil
		case "evil.example/@luna":
			// Claims to be maid's actor; the canonical refetch decides.
			return outboundResponse(200, misskeyActor), nil
		case "liar.example/@bob":
			return outboundResponse(200, `{"id":"https://liar.example/users/bob","type":"Person","preferredUsername":"bob"}`), nil
		case "liar.example/users/bob":
			return outboundResponse(200, `{"id":"https://liar.example/users/someone-else","type":"Person","preferredUsername":"bob"}`), nil
		}
		return outboundResponse(404, ""), nil
	}
}

// searchFederationDB opens SQLite (or PostgreSQL via TEST_DATABASE_URL) with
// integer primary keys so remote records get ids on SQLite, as the
// production migrations do.
func searchFederationDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	all := []any{&models.Account{}, &models.AccountExtra{}, &models.AccountKey{}, &models.RemoteActorIdentity{}, &models.AccountEmoji{}, &models.Emoji{},
		&models.Status{}, &models.StatusContent{}, &models.StatusToAccount{}, &models.Tag{}, &models.StatusTag{}, &models.StatusEmoji{}, &models.MediaAttachment{},
		&models.Follow{}, &models.Block{}, &models.SystemSetting{}, &models.Poll{}, &models.PollOption{}, &models.Conversation{}, &models.AccountConversation{},
		&models.Notification{}, &models.FederationOutbox{}}
	if conn.Dialector.Name() == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
		for _, m := range all {
			stmt := &gorm.Statement{DB: conn}
			require.NoError(t, stmt.Parse(m))
			for _, field := range stmt.Schema.Fields {
				if field.AutoIncrement {
					field.DataType = schema.Int
				}
			}
		}
	}
	require.NoError(t, conn.AutoMigrate(all...))
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sqlDB.Close() })
	return conn
}

func setupSearchFederation(t *testing.T) (*gorm.DB, *fakeFediverse) {
	t.Helper()
	conn := searchFederationDB(t)
	systemsettings.Set(systemsettings.KeyDomain, "local.example")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	fake := &fakeFediverse{key: key}
	oldClient, oldSigner := sharedHTTPClient, systemSigningClient
	t.Cleanup(func() { sharedHTTPClient, systemSigningClient = oldClient, oldSigner })
	sharedHTTPClient = &http.Client{Transport: outboundTransport(fake.serve(t))}
	systemSigningClient = func(context.Context) (*Client, error) {
		return NewClient(key, "https://local.example/users/system.proxy"), nil
	}
	return conn, fake
}

func TestResolveSearchAccountMisskeyByAcct(t *testing.T) {
	conn, _ := setupSearchFederation(t)
	// A local account with the same username must not be confused with it.
	require.NoError(t, conn.Create(&models.Account{Username: "luna"}).Error)

	account, err := ResolveSearchAccount(context.Background(), "@luna@maid.example")
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, "luna", account.Username)
	require.Equal(t, "maid.example", *account.Domain)
	require.Equal(t, "https://maid.example/users/9h6zahxdlu", account.OriginId)
	require.Equal(t, "https://files.maid.example/a.webp", account.Avatar)

	// Profile URL resolves to the same stored account.
	byURL, status, err := ResolveSearchURL(context.Background(), "https://maid.example/@luna")
	require.NoError(t, err)
	require.Nil(t, status)
	require.Equal(t, account.Id, byURL.Id)

	var count int64
	require.NoError(t, conn.Model(&models.Account{}).Where("username = ?", "luna").Count(&count).Error)
	require.EqualValues(t, 2, count)
}

func TestResolveSearchURLMisskeyNote(t *testing.T) {
	setupSearchFederation(t)
	account, status, err := ResolveSearchURL(context.Background(), "https://maid.example/notes/a1b2c3")
	require.NoError(t, err)
	require.Nil(t, account)
	require.NotNil(t, status)
	require.Equal(t, "https://maid.example/notes/a1b2c3", *status.OriginUri)
	require.Contains(t, status.Content.Content, "hello from misskey")
}

func TestResolveSearchMastodonAuthorizedFetch(t *testing.T) {
	_, fake := setupSearchFederation(t)
	account, err := ResolveSearchAccount(context.Background(), "@Gargron@masto.example")
	require.NoError(t, err)
	require.Equal(t, "Gargron", account.Username)
	require.Equal(t, "masto.example", *account.Domain)
	require.Positive(t, fake.unsigned.Load(), "the first GET is unsigned")
	require.Positive(t, fake.signed.Load(), "the 401 is retried with a signature")

	// A web status URL resolves to the canonical ActivityPub id.
	_, status, err := ResolveSearchURL(context.Background(), "https://masto.example/@Gargron/123")
	require.NoError(t, err)
	require.NotNil(t, status)
	require.Equal(t, "https://masto.example/users/Gargron/statuses/123", *status.OriginUri)
	require.Equal(t, account.Id, status.AccountId)
}

func TestResolveSearchURLCanonicalIdentity(t *testing.T) {
	setupSearchFederation(t)
	// evil.example serving maid's actor only yields maid's genuine actor,
	// fetched from maid itself.
	account, _, err := ResolveSearchURL(context.Background(), "https://evil.example/@luna")
	require.NoError(t, err)
	require.Equal(t, "https://maid.example/users/9h6zahxdlu", account.OriginId)

	// A canonical id that does not identify itself is rejected.
	_, _, err = ResolveSearchURL(context.Background(), "https://liar.example/@bob")
	require.Error(t, err)
	_, err = ResolveSearchAccount(context.Background(), "https://liar.example/@bob")
	require.Error(t, err)
}

func TestResolveSearchAccountRejectsNonActor(t *testing.T) {
	conn, _ := setupSearchFederation(t)
	_, err := ResolveSearchAccount(context.Background(), "https://maid.example/notes/a1b2c3")
	require.Error(t, err)
	var count int64
	require.NoError(t, conn.Model(&models.Account{}).Count(&count).Error)
	require.Zero(t, count, "a Note must never be stored as an account")
}

func TestResolveSearchSkipsOwnDomain(t *testing.T) {
	setupSearchFederation(t)
	account, err := ResolveSearchAccount(context.Background(), "@luna@local.example")
	require.NoError(t, err)
	require.Nil(t, account)
	account, status, err := ResolveSearchURL(context.Background(), "https://local.example/@luna")
	require.NoError(t, err)
	require.Nil(t, account)
	require.Nil(t, status)
}

func TestParseAcctQuery(t *testing.T) {
	systemsettings.Set(systemsettings.KeyDomain, "local.example")
	for _, tc := range []struct {
		query        string
		user, domain string
		ok           bool
	}{
		{"@luna@maid.lat", "luna", "maid.lat", true},
		{"luna@Maid.LAT", "luna", "maid.lat", true},
		{"acct:luna@maid.lat", "luna", "maid.lat", true},
		{"@luna@local.example", "luna", "", true},
		{"luna", "", "", false},
		{"@luna", "", "", false},
		{"https://maid.lat/@luna", "", "", false},
		{"luna@maid.lat extra", "", "", false},
	} {
		got, ok := ParseAcctQuery(tc.query)
		require.Equal(t, tc.ok, ok, tc.query)
		if ok {
			require.Equal(t, AcctQuery{Username: tc.user, Domain: tc.domain}, got, tc.query)
		}
	}
}

func TestActorUnmarshalTolerantShapes(t *testing.T) {
	var actor Actor
	require.NoError(t, json.Unmarshal([]byte(misskeyActor), &actor))
	require.Equal(t, "https://old.example/users/luna", actor.AlsoKnownAs)
	require.Equal(t, "https://maid.example/@luna", actor.URL)
	require.Equal(t, "https://files.maid.example/a.webp", actor.Icon.URL)
	require.Equal(t, "https://maid.example/inbox", actor.Endpoints.SharedInbox)

	require.NoError(t, json.Unmarshal([]byte(`{"id":"https://x.example/u","type":"Person",
		"url":[{"type":"Link","mediaType":"application/activity+json","href":"https://x.example/u"},{"type":"Link","mediaType":"text/html","href":"https://x.example/@u"}],
		"icon":[{"type":"Image","url":{"type":"Link","href":"https://x.example/a.png"}}],"image":"https://x.example/h.png","name":null,
		"alsoKnownAs":"https://y.example/u","movedTo":{"id":"https://z.example/u"}}`), &actor))
	require.Equal(t, "https://x.example/@u", actor.URL)
	require.Equal(t, "https://x.example/a.png", actor.Icon.URL)
	require.Equal(t, "https://x.example/h.png", actor.Image.URL)
	require.Equal(t, "https://y.example/u", actor.AlsoKnownAs)
	require.Equal(t, "https://z.example/u", actor.MovedTo)
	require.Empty(t, actor.Name)
}

func TestIsActivityPubMediaType(t *testing.T) {
	require.True(t, isActivityPubMediaType("application/activity+json"))
	require.True(t, isActivityPubMediaType(`application/ld+json; profile="https://www.w3.org/ns/activitystreams"`))
	require.False(t, isActivityPubMediaType("text/html"))
}
