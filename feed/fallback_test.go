package feed

import (
	"context"
	"github.com/redis/go-redis/v9"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRedisOutageUsesBoundedSQLAndCurrentVisibility(t *testing.T) {
	_, client, service, timeline := protocolFixture(t)
	require.NoError(t, client.Close())
	previousService, previousOverride := defaultService, defaultServiceOverride
	SetDefaultService(service)
	t.Cleanup(func() { defaultService = previousService; defaultServiceOverride = previousOverride })
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	previousDB := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previousDB); _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Status{}, &models.Account{}, &models.AccountExtra{}, &models.StatusContent{}, &models.Application{}, &models.Attachment{}, &models.Emoji{}, &models.Poll{}, &models.PollOption{}, &models.Tag{}, &models.DomainBlock{}, &models.Block{}, &models.Follow{}, &models.MuteAccount{}))
	write := conn.Session(&gorm.Session{SkipHooks: true})
	require.NoError(t, write.Create(&models.Account{Id: 1, Username: "fallback-author"}).Error)
	require.NoError(t, write.Create(&models.AccountExtra{AccountId: 1}).Error)
	require.NoError(t, write.Create(&models.Status{Id: 1, AccountId: 1, Visibility: models.StatusVisibilityPublic}).Error)
	require.NoError(t, write.Create(&models.Status{Id: 2, AccountId: 1, Visibility: models.StatusVisibilityDirect}).Error)
	filter := func(q *gorm.DB) (*gorm.DB, error) { return q, nil }
	got, err := GetStatusTimelineContent(timeline, &ListParam{Context: context.Background(), Limit: 10}, filter, nil)
	require.NoError(t, err)
	require.Equal(t, []uint64{1}, got.GetIds(), "outage must neither return false empty nor expose a private candidate")
	// A connected but non-responsive Redis is different from refusal: socket
	// reads must obey the shared context rather than the default five seconds.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var sockets sync.Map
	accepted := make(chan struct{}, 1)
	t.Cleanup(func() {
		_ = listener.Close()
		sockets.Range(func(key, _ any) bool { _ = key.(net.Conn).Close(); return true })
	})
	go func() {
		for {
			socket, err := listener.Accept()
			if err != nil {
				return
			}
			sockets.Store(socket, true)
			select {
			case accepted <- struct{}{}:
			default:
			}
			go func() { _, _ = io.Copy(io.Discard, socket); sockets.Delete(socket); _ = socket.Close() }()
		}
	}()
	stalled := redis.NewClient(&redis.Options{Addr: listener.Addr().String(), ContextTimeoutEnabled: true, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxRetries: -1})
	t.Cleanup(func() { _ = stalled.Close() })
	SetDefaultService(NewFeedService(NewRedisClient(stalled)))
	started := time.Now()
	got, err = GetStatusTimelineContent(timeline, &ListParam{Context: context.Background(), Limit: 10}, filter, nil)
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.Equal(t, []uint64{1}, got.GetIds(), "paused Redis still applies SQL visibility")
	require.Less(t, elapsed, 1500*time.Millisecond, "optional Redis work must not spend five seconds before fallback")
	select {
	case <-accepted:
	default:
		t.Fatal("test did not reach TCP-connected Redis")
	}
	SetDefaultService(service)
	for range cap(fallbackSlots) {
		fallbackSlots <- struct{}{}
	}
	defer func() {
		for range cap(fallbackSlots) {
			<-fallbackSlots
		}
	}()
	_, err = GetStatusTimelineContent(timeline, &ListParam{Context: context.Background(), Limit: 10}, filter, nil)
	require.ErrorIs(t, err, ErrFeedUnavailable, "SQL admission must fail rather than queue unbounded work")
}
