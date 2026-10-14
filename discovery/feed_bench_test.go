package discovery

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// TestFeedRealisticVolume is an opt-in profile (FEED_BENCH=1) with prod-like
// volume: ~22k statuses over 7 days, ~2900 remote authors (176 opted in).
func TestFeedRealisticVolume(t *testing.T) {
	if os.Getenv("FEED_BENCH") == "" {
		t.Skip("set FEED_BENCH=1")
	}
	feedTestRedis(t)
	conn, viewer := feedTestDatabase(t)
	require.NoError(t, conn.AutoMigrate(&models.BookmarkStatus{}, &models.StatusCard{}, &models.PinStatus{}))
	write := conn.Session(&gorm.Session{SkipHooks: true, CreateBatchSize: 500})
	now := time.Now().UTC()
	yes := true
	accounts := []models.Account{}
	for i := uint64(1000); i < 3900; i++ {
		d := fmt.Sprintf("host%d.example", i%150)
		a := models.Account{Id: i, Username: fmt.Sprintf("r%d", i), Acct: fmt.Sprintf("r%d@%s", i, d), Domain: &d, OriginId: fmt.Sprintf("https://%s/u/%d", d, i)}
		if i%16 == 0 {
			a.RemoteDiscoverable = &yes
			a.RemoteDiscoveryCheckedAt = &now
			a.RemoteDiscoverySource = "actor"
		}
		accounts = append(accounts, a)
	}
	require.NoError(t, write.Omit("Extra", "User").Create(&accounts).Error)
	tags := []models.Tag{}
	for i := uint64(1); i <= 300; i++ {
		tags = append(tags, models.Tag{Id: i, Name: fmt.Sprintf("tag%d", i), NameKey: fmt.Sprintf("tag%d", i)})
	}
	require.NoError(t, write.Create(&tags).Error)
	statuses, contents, links := []models.Status{}, []models.StatusContent{}, []models.StatusTag{}
	for i := uint64(1000); i < 23000; i++ {
		created := now.Add(-time.Duration(i-1000) * 27 * time.Second)
		author := 1000 + (i*7)%2900
		d := accounts[author-1000].Domain
		statuses = append(statuses, models.Status{Id: i, AccountId: author, Domain: d, Visibility: models.StatusVisibilityPublic, CreatedAt: &created})
		contents = append(contents, models.StatusContent{StatusId: i, Content: fmt.Sprintf("post %d", i)})
		links = append(links, models.StatusTag{StatusId: i, TagId: 1 + i%300})
	}
	require.NoError(t, write.Omit("Content").Create(&statuses).Error)
	require.NoError(t, write.Create(&contents).Error)
	require.NoError(t, write.Create(&links).Error)

	var queries atomic.Int64
	require.NoError(t, conn.Callback().Query().Before("gorm:query").Register("bench:count", func(*gorm.DB) { queries.Add(1) }))
	require.NoError(t, conn.Callback().Raw().Before("gorm:raw").Register("bench:countraw", func(*gorm.DB) { queries.Add(1) }))
	measure := func(name string, f func() error) {
		queries.Store(0)
		start := time.Now()
		err := f()
		t.Logf("%-28s %8s  queries=%d err=%v", name, time.Since(start).Round(time.Millisecond), queries.Load(), err)
	}
	ctx := context.Background()
	measure("RefreshFeedCandidates", func() error { return RefreshFeedCandidates(ctx) })
	var page Page
	serialize := func(p Page) error {
		ss := models.Statuses{}
		for _, it := range p.Items {
			ss = append(ss, it.Status)
		}
		_, err := serializer.ToStatusesContext(models.WithFilterContext(ctx, "home"), ss, viewer)
		return err
	}
	for _, pref := range []string{"generic", "personalized"} {
		_, err := models.SetRecommendationPreference(conn, viewer.UserId, pref)
		require.NoError(t, err)
		measure(pref+" first page", func() (err error) { page, err = StatusPage(ctx, viewer, "", 20); return })
		t.Logf("  items=%d mode=%s", len(page.Items), page.Mode)
		measure(pref+" serialize", func() error { return serialize(page) })
		for i := 0; i < 3 && page.NextCursor != nil; i++ {
			measure(fmt.Sprintf("%s page %d", pref, i+2), func() (err error) { page, err = StatusPage(ctx, viewer, *page.NextCursor, 20); return })
			t.Logf("  items=%d", len(page.Items))
		}
	}
}
