package linkcard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSuccessfulHTMLWithoutMetadataGetsTruthfulSiteCard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/blocked" {
			w.WriteHeader(403)
		}
		_, _ = w.Write([]byte("<html><head></head><body>JavaScript required</body></html>"))
	}))
	defer srv.Close()
	m, err := plainFetcher().FetchMeta(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, "127.0.0.1", m.Title)
	require.Equal(t, srv.URL, m.ProviderURL)
	require.Empty(t, m.Image)
	require.Empty(t, m.Description)
	require.Equal(t, "link", m.Type)
	_, err = plainFetcher().FetchMeta(context.Background(), srv.URL+"/blocked")
	require.Error(t, err)
}

func TestQueueDeduplicatesPendingAndDoesNotRetainOverflow(t *testing.T) {
	q := &queue{jobs: make(chan uint64, 1), pending: map[uint64]bool{}}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); q.enqueue(1) }()
	}
	wg.Wait()
	require.Len(t, q.jobs, 1)
	require.Len(t, q.pending, 1)
	require.False(t, q.enqueue(2))
	require.False(t, q.pending[2])
	require.EqualValues(t, 1, <-q.jobs)
	q.mu.Lock()
	delete(q.pending, 1)
	q.mu.Unlock()
	require.True(t, q.enqueue(2))
	require.EqualValues(t, 2, <-q.jobs)
}

func TestCardAndContentMirrorCommitTogetherAndRetry(t *testing.T) {
	e := newEnv(t)
	st := e.status(t, e.srv.URL+"/page")
	require.NoError(t, e.conn.Callback().Update().Before("gorm:update").Register("test:card_mirror_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "status_contents" {
			tx.AddError(errors.New("injected content mirror failure"))
		}
	}))
	_, err := e.svc.Process(context.Background(), st.Id)
	require.Error(t, err)
	var n int64
	require.NoError(t, e.conn.Model(&models.StatusCard{}).Where("status_id = ?", st.Id).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, e.conn.Callback().Update().Remove("test:card_mirror_failure"))
	out, err := e.svc.Process(context.Background(), st.Id)
	require.NoError(t, err)
	require.Equal(t, Created, out)
	var content models.StatusContent
	require.NoError(t, e.conn.First(&content, st.Id).Error)
	require.Equal(t, "Hello & World", content.Card["title"])
}

func TestFirstLinkKeepsBilibiliAndShortURLIntact(t *testing.T) {
	for _, link := range []string{"https://www.bilibili.com/video/BV169aw6dEPg", "https://b23.tv/Ab60xs", "https://example.org/video?s=6&x=0"} {
		require.Equal(t, link, FirstLink(link))
		require.Equal(t, link, FirstLink("<p>"+link+"</p>"))
	}
}

func TestExistingCardRepairsMissingHistoricalContentMirror(t *testing.T) {
	e := newEnv(t)
	st := e.status(t, e.srv.URL+"/page")
	_, err := e.svc.Process(context.Background(), st.Id)
	require.NoError(t, err)
	require.NoError(t, e.conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.StatusContent{}).Where("status_id = ?", st.Id).UpdateColumn("card", nil).Error)
	out, err := e.svc.Process(context.Background(), st.Id)
	require.NoError(t, err)
	require.Equal(t, Skipped, out)
	var content models.StatusContent
	require.NoError(t, e.conn.First(&content, st.Id).Error)
	require.Equal(t, "Hello & World", content.Card["title"])
}

func TestLargeHTMLBodyKeepsAlreadyParsedHeadMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><meta property="og:title" content="Real video title"></head><body>` + strings.Repeat("x", MaxPageBytes+1024) + `</body></html>`))
	}))
	defer srv.Close()
	meta, err := plainFetcher().FetchMeta(context.Background(), srv.URL)
	require.NoError(t, err)
	require.Equal(t, "Real video title", meta.Title)
}

func TestProcessWithoutAvailableDatabaseReturnsError(t *testing.T) {
	for _, svc := range []*Service{nil, {}, {DB: func() *gorm.DB { return nil }}} {
		result, err := svc.Process(context.Background(), 1)
		require.ErrorContains(t, err, "database unavailable")
		require.Equal(t, Skipped, result)
	}
}
