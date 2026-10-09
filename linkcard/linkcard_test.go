package linkcard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMain(m *testing.M) {
	database.SetTestMode()
	_ = os.Setenv("LINK_CARDS_ENABLED", "false")
	os.Exit(m.Run())
}

func TestFirstLink(t *testing.T) {
	cases := map[string]string{
		`<p><span class="h-card"><a href="https://x.example/@bob" class="u-url mention">@bob</a></span> <a href="https://x.example/tags/go" class="mention hashtag" rel="tag">#go</a> <a href="https://news.example/a?b=1#frag" rel="nofollow noopener">news</a> <a href="https://second.example">2</a></p>`: "https://news.example/a?b=1",
		`<p><a href="javascript:alert(1)">x</a> <a href="ftp://h/f">f</a> <a href="https://u:p@h.example/">cred</a></p>`: "",
		`plain https://a.example/path, then https://b.example`:                                                           "https://a.example/path",
		`<p>unlinked https://c.example/x.</p>`:                                                                           "https://c.example/x",
		`no links at all`:                                                                                                "",
	}
	for in, want := range cases {
		assert.Equal(t, want, FirstLink(in), in)
	}
}

const page = `<!doctype html><html><head><title>Fallback</title>
<meta property="og:title" content="  Hello &amp; <b>World</b> ">
<meta property="og:description" content="A description">
<meta property="og:image" content="/img/p.png">
<meta property="og:site_name" content="Example News">
<meta name="author" content="Jane">
<link rel="alternate" type="application/json+oembed" href="/oembed">
</head><body><meta property="og:title" content="late"></body></html>`

func newPageServer(t *testing.T, png []byte) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("/oembed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"type":"rich","html":"<script>x</script>","author_name":"Ignored","author_url":"https://a.example/jane","provider_url":"https://example.org"}`))
	})
	mux.HandleFunc("/img/p.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(png)
	})
	mux.HandleFunc("/pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF"))
	})
	mux.HandleFunc("/huge", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprint(w, "<html><head>"+strings.Repeat("<!-- x -->", 400000)+`<meta property="og:title" content="too late"></head>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

var tinyPNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xff\xff?\x00\x05\xfe\x02\xfe\xa7\x9a\xa0\xa0\x00\x00\x00\x00IEND\xaeB`\x82")

func plainFetcher() *Fetcher { return &Fetcher{Client: &http.Client{Timeout: 5 * time.Second}} }

func TestFetchMeta(t *testing.T) {
	srv := newPageServer(t, tinyPNG)
	f := plainFetcher()
	m, err := f.FetchMeta(context.Background(), srv.URL+"/page")
	require.NoError(t, err)
	assert.Equal(t, "Hello & World", m.Title, "markup stripped, whitespace trimmed")
	assert.Equal(t, "A description", m.Description)
	assert.Equal(t, srv.URL+"/img/p.png", m.Image, "relative image resolved")
	assert.Equal(t, "Example News", m.ProviderName)
	assert.Equal(t, "Jane", m.AuthorName, "page author wins over oEmbed")
	assert.Equal(t, "https://a.example/jane", m.AuthorURL)
	assert.Equal(t, "https://example.org", m.ProviderURL)
	assert.Equal(t, "link", m.Type, "oEmbed rich HTML is never adopted")

	_, err = f.FetchMeta(context.Background(), srv.URL+"/pdf")
	assert.ErrorIs(t, err, ErrNotHTML)
	_, err = f.FetchMeta(context.Background(), srv.URL+"/huge")
	assert.Error(t, err, "metadata beyond the 1MB cap is not read")
	_, err = f.FetchMeta(context.Background(), "ftp://example.com/x")
	assert.Error(t, err)
	_, err = f.FetchMeta(context.Background(), srv.URL+"/missing")
	assert.Error(t, err)
}

func TestSSRFProtection(t *testing.T) {
	srv := newPageServer(t, tinyPNG)
	safe := &Fetcher{Client: activitypub.NewSafeHTTPClient(5*time.Second, 3), Validate: activitypub.ValidateURI}
	ctx := context.Background()
	for _, u := range []string{
		srv.URL + "/page", // httptest binds to 127.0.0.1
		"http://127.0.0.1:1/", "http://localhost/", "http://[::1]/", "http://10.0.0.5/", "http://192.168.1.1/",
		"http://169.254.169.254/latest/meta-data/", "http://0.0.0.0/", "http://[::ffff:127.0.0.1]/",
		"http://user:pw@example.com/", "file:///etc/passwd", "gopher://example.com/",
	} {
		_, err := safe.FetchMeta(ctx, u)
		assert.Error(t, err, u)
	}

	// The client itself (without the URL pre-check) must also refuse private
	// peers, including redirects that lead to them.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	t.Cleanup(redirector.Close)
	bare := &Fetcher{Client: activitypub.NewSafeHTTPClient(5*time.Second, 3)}
	_, err := bare.FetchMeta(ctx, "http://127.0.0.1:1/")
	assert.Error(t, err)
	_, err = bare.FetchMeta(ctx, redirector.URL)
	assert.Error(t, err)

	// Redirect limit and non-HTTP redirect targets on a plain-dialer client
	// built with the same redirect policy.
	var hops int32
	loop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hops, 1)
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	t.Cleanup(loop.Close)
	policy := activitypub.NewSafeHTTPClient(5*time.Second, 3)
	policy.Transport = http.DefaultTransport
	_, err = (&Fetcher{Client: policy}).FetchMeta(ctx, loop.URL)
	assert.Error(t, err)
	assert.LessOrEqual(t, atomic.LoadInt32(&hops), int32(5), "redirect chain is cut")

	ftp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://example.com/x", http.StatusFound)
	}))
	t.Cleanup(ftp.Close)
	_, err = (&Fetcher{Client: policy}).FetchMeta(ctx, ftp.URL)
	assert.Error(t, err)

	// Image downloads go through the same protection.
	_, _, err = activitypub.FetchRemoteMedia(ctx, srv.URL+"/img/p.png", MaxImageBytes)
	assert.Error(t, err)
}

type env struct {
	svc   *Service
	conn  *gorm.DB
	fix   *testutil.FixtureBuilder
	acc   *models.Account
	srv   *httptest.Server
	saved map[string][]byte
	pages int32
}

func newEnv(t *testing.T) *env {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(
		&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.AccountEmoji{}, &models.Emoji{},
		&models.Status{}, &models.StatusContent{}, &models.StatusHistory{}, &models.StatusTag{}, &models.Tag{},
		&models.StatusToAccount{}, &models.StatusEmoji{}, &models.MediaAttachment{}, &models.DomainBlock{}, &models.StatusCard{},
		&models.Conversation{}, &models.AccountConversation{},
	))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	e := &env{conn: conn, fix: testutil.NewFixtureBuilder(t, conn), saved: map[string][]byte{}}
	backend := newPageServer(t, tinyPNG)
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&e.pages, 1)
		http.Redirect(w, r, backend.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(e.srv.Close)
	e.svc = &Service{
		DB:      func() *gorm.DB { return e.conn },
		Fetcher: plainFetcher(),
		FetchImage: func(ctx context.Context, raw string, limit int64) ([]byte, string, error) {
			return tinyPNG, "image/png", nil
		},
		SaveImage: func(key string, data []byte) error { e.saved[key] = data; return nil },
		BaseURL:   func() string { return "https://home.example" },
	}
	user := e.fix.CreateUser()
	e.acc = e.fix.CreateAccount(user)
	return e
}

func (e *env) status(t *testing.T, content string, mod ...func(*models.Status)) *models.Status {
	st := e.fix.CreateStatus(e.acc, append([]func(*models.Status){func(s *models.Status) {
		s.Content = models.StatusContent{Content: content}
	}}, mod...)...)
	return st
}

func TestProcessCreatesCard(t *testing.T) {
	e := newEnv(t)
	st := e.status(t, `<p><a href="`+e.srv.URL+`/page">link</a></p>`)
	out, err := e.svc.Process(context.Background(), st.Id)
	require.NoError(t, err)
	assert.Equal(t, Created, out)

	var card models.StatusCard
	require.NoError(t, e.conn.Where("status_id = ?", st.Id).First(&card).Error)
	assert.Equal(t, "Hello & World", card.Title)
	assert.Regexp(t, `^https://home\.example/card-images/[0-9a-f]{32}\.png$`, card.ImageUrl)
	require.Len(t, e.saved, 1)
	for k := range e.saved {
		assert.True(t, strings.HasPrefix(k, "cards/"))
	}

	var content models.StatusContent
	require.NoError(t, e.conn.First(&content, st.Id).Error)
	assert.Equal(t, "Hello & World", content.Card["title"])
	raw, _ := json.Marshal(content.Card)
	assert.Contains(t, string(raw), `"provider_name":"Example News"`)
	assert.Equal(t, "<p><a href=\""+e.srv.URL+"/page\">link</a></p>", content.Content, "content is untouched")

	// Same status again: nothing to do.
	out, err = e.svc.Process(context.Background(), st.Id)
	require.NoError(t, err)
	assert.Equal(t, Skipped, out)

	// A second status with the same URL reuses the card without fetching.
	before := atomic.LoadInt32(&e.pages)
	st2 := e.status(t, `see `+e.srv.URL+`/page`)
	out, err = e.svc.Process(context.Background(), st2.Id)
	require.NoError(t, err)
	assert.Equal(t, Reused, out)
	assert.Equal(t, before, atomic.LoadInt32(&e.pages))
	assert.Len(t, e.saved, 1, "image is shared")
}

func TestProcessSkips(t *testing.T) {
	e := newEnv(t)
	link := `<a href="` + e.srv.URL + `/page">x</a>`
	skipped := func(st *models.Status) {
		out, err := e.svc.Process(context.Background(), st.Id)
		require.NoError(t, err)
		assert.Equal(t, Skipped, out)
		var n int64
		e.conn.Model(&models.StatusCard{}).Where("status_id = ?", st.Id).Count(&n)
		assert.Zero(t, n)
	}
	boosted := e.status(t, link)
	rid := boosted.Id
	skipped(e.status(t, link, func(s *models.Status) { s.ReblogId = &rid }))
	skipped(e.status(t, link, func(s *models.Status) { s.Visibility = uint8(models.StatusVisibilityDirect) }))
	skipped(e.status(t, `only a <a class="mention" href="https://x.example/@a">@a</a> #tag`))

	withMedia := e.status(t, link)
	require.NoError(t, e.conn.Exec("INSERT INTO media_attachments (account_id, status_id, file_name, file_path, file_size, content_type) VALUES (?, ?, 'a.png', 'a.png', 1, 'image/png')", e.acc.Id, withMedia.Id).Error)
	skipped(withMedia)

	d := "bad.example"
	require.NoError(t, e.conn.Create(&models.DomainBlock{Domain: "bad.example", Severity: "suspend"}).Error)
	skipped(e.status(t, link, func(s *models.Status) { s.Domain = &d }))
	sub := "sub.bad.example"
	skipped(e.status(t, link, func(s *models.Status) { s.Domain = &sub }))
	assert.Zero(t, atomic.LoadInt32(&e.pages), "nothing was fetched")
}

func TestProcessFailureIsCachedAndNotAnError(t *testing.T) {
	e := newEnv(t)
	st := e.status(t, `<a href="`+e.srv.URL+`/pdf">x</a>`)
	out, err := e.svc.Process(context.Background(), st.Id)
	require.NoError(t, err)
	assert.Equal(t, Failed, out)
	before := atomic.LoadInt32(&e.pages)
	st2 := e.status(t, `<a href="`+e.srv.URL+`/pdf">x</a>`)
	out, _ = e.svc.Process(context.Background(), st2.Id)
	assert.Equal(t, Failed, out)
	assert.Equal(t, before, atomic.LoadInt32(&e.pages), "failed URLs are not retried right away")
}

func TestBadImageIsDropped(t *testing.T) {
	e := newEnv(t)
	e.svc.FetchImage = func(context.Context, string, int64) ([]byte, string, error) {
		return []byte("<svg onload=alert(1)></svg>"), "image/svg+xml", nil
	}
	st := e.status(t, `<a href="`+e.srv.URL+`/page">x</a>`)
	out, err := e.svc.Process(context.Background(), st.Id)
	require.NoError(t, err)
	assert.Equal(t, Created, out)
	var card models.StatusCard
	require.NoError(t, e.conn.Where("status_id = ?", st.Id).First(&card).Error)
	assert.Empty(t, card.ImageUrl)
	assert.Empty(t, e.saved)
}

func TestBackfill(t *testing.T) {
	e := newEnv(t)
	a := e.status(t, `<a href="`+e.srv.URL+`/page">x</a>`)
	e.status(t, `no link here`)
	b := e.status(t, `<a href="`+e.srv.URL+`/pdf">x</a>`)
	old := e.status(t, `<a href="`+e.srv.URL+`/page">old</a>`)
	require.NoError(t, e.conn.Model(&models.Status{}).Where("id = ?", old.Id).Update("created_at", time.Now().AddDate(0, 0, -30)).Error)

	stats, err := e.svc.Backfill(context.Background(), 7, 100, 3, 0, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 2, stats.Candidates)
	assert.EqualValues(t, 1, stats.Created+stats.Reused)
	assert.EqualValues(t, 1, stats.Failed)
	var n int64
	e.conn.Model(&models.StatusCard{}).Where("status_id IN ?", []uint64{a.Id, b.Id, old.Id}).Count(&n)
	assert.EqualValues(t, 1, n)

	stats, err = e.svc.Backfill(context.Background(), 0, 1, 1, 0, nil)
	require.NoError(t, err)
	assert.EqualValues(t, 1, stats.Candidates, "limit applies; --days 0 includes old statuses")
}

func TestStartQueueDropsWhenFull(t *testing.T) {
	block := make(chan struct{})
	svc := &Service{DB: func() *gorm.DB { <-block; return nil }}
	cur := startQueue(svc, 1, 2, 1000)
	defer close(block)
	accepted := 0
	for i := 1; i <= 10; i++ {
		select {
		case cur.jobs <- uint64(i):
			accepted++
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.LessOrEqual(t, accepted, 4, "queue is capped")
}
