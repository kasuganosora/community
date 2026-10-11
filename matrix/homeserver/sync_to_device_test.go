package homeserver

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func syncInboxTestDB(t *testing.T) (*gorm.DB, *SyncService) {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(filepath.Join(t.TempDir(), "sync-inbox.sqlite")), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.AutoMigrate(&MatrixToDeviceMessage{}, &MatrixSyncToDeviceBatch{}, &MatrixUserSession{}))
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sqlDB.Close() })
	return conn, &SyncService{connection: conn}
}
func enqueueSyncKeys(t *testing.T, conn *gorm.DB, user, device string, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		require.NoError(t, conn.Create(&MatrixToDeviceMessage{Sender: "@sender:test", TargetUser: user, TargetDevice: device, EventType: "m.room.encrypted", Content: map[string]any{"ciphertext": fmt.Sprint(i)}}).Error)
	}
}

func TestToDeviceAcknowledgedBatchSurvivesLostResponse(t *testing.T) {
	conn, service := syncInboxTestDB(t)
	const user = "@ack:test"
	enqueueSyncKeys(t, conn, user, "A", 101)
	first, batch, err := service.getToDeviceMessages(user, "A", "s123")
	require.NoError(t, err)
	require.Len(t, first.Events, 100)
	require.NotEmpty(t, batch)
	var count int64
	require.NoError(t, conn.Model(&MatrixToDeviceMessage{}).Count(&count).Error)
	require.Equal(t, int64(101), count, "forming a response must not consume keys")
	// Simulate cancellation/disconnect after the transaction committed: discard
	// first, then repeat the exact request on a fresh service (server restart).
	restarted := &SyncService{connection: conn}
	retry, same, err := restarted.getToDeviceMessages(user, "A", "s123")
	require.NoError(t, err)
	require.Equal(t, batch, same)
	require.Equal(t, first, retry)
	// New arrival is not part of the outstanding 100-row snapshot.
	enqueueSyncKeys(t, conn, user, "A", 1)
	next, nextBatch, err := restarted.getToDeviceMessages(user, "A", "s123_d"+batch)
	require.NoError(t, err)
	require.Len(t, next.Events, 2)
	require.NotEqual(t, batch, nextBatch)
	require.NoError(t, conn.Model(&MatrixToDeviceMessage{}).Count(&count).Error)
	require.Equal(t, int64(2), count)
	// The same millisecond and stale ACK cannot acknowledge a different batch.
	again, sameNext, err := restarted.getToDeviceMessages(user, "A", "s123_d"+batch)
	require.NoError(t, err)
	require.Equal(t, nextBatch, sameNext)
	require.Equal(t, next, again)
	empty, emptyBatch, err := restarted.getToDeviceMessages(user, "A", "s123_d"+nextBatch)
	require.NoError(t, err)
	require.Empty(t, empty.Events)
	require.Empty(t, emptyBatch)
	require.NoError(t, conn.Model(&MatrixToDeviceMessage{}).Count(&count).Error)
	require.Zero(t, count)
	require.Equal(t, int64(123), service.parseSyncToken("s123_d"+batch))
	require.Equal(t, int64(123), service.parseSyncToken("s123"))
}

func TestToDeviceBatchDeviceIsolationAndCancellation(t *testing.T) {
	conn, service := syncInboxTestDB(t)
	enqueueSyncKeys(t, conn, "@one:test", "A", 1)
	enqueueSyncKeys(t, conn, "@one:test", "B", 1)
	enqueueSyncKeys(t, conn, "@two:test", "A", 1)
	_, a, err := service.getToDeviceMessages("@one:test", "A", "")
	require.NoError(t, err)
	for _, target := range [][2]string{{"@one:test", "B"}, {"@two:test", "A"}} {
		own, b, err := service.getToDeviceMessages(target[0], target[1], "s1_d"+a)
		require.NoError(t, err)
		require.Len(t, own.Events, 1)
		require.NotEqual(t, a, b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := &SyncService{connection: conn.WithContext(ctx)}
	_, _, err = canceled.getToDeviceMessages("@one:test", "A", "s1_d"+a)
	require.ErrorIs(t, err, context.Canceled)
	retry, b, err := service.getToDeviceMessages("@one:test", "A", "")
	require.NoError(t, err)
	require.Len(t, retry.Events, 1)
	require.Equal(t, a, b)
}

func TestToDeviceConcurrentSyncSharesPendingBatch(t *testing.T) {
	conn, service := syncInboxTestDB(t)
	enqueueSyncKeys(t, conn, "@concurrent:test", "A", 10)
	const workers = 12
	var wg sync.WaitGroup
	batches := make(chan string, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			events, batch, err := service.getToDeviceMessages("@concurrent:test", "A", "s9")
			if err == nil && len(events.Events) != 10 {
				err = fmt.Errorf("got %d events", len(events.Events))
			}
			batches <- batch
			errs <- err
		}()
	}
	wg.Wait()
	close(batches)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var previous string
	for batch := range batches {
		require.NotEmpty(t, batch)
		if previous != "" {
			require.Equal(t, previous, batch)
		}
		previous = batch
	}
	var count int64
	require.NoError(t, conn.Model(&MatrixToDeviceMessage{}).Count(&count).Error)
	require.Equal(t, int64(10), count)
}

func TestToDeviceLogoutClearsPendingBatchWithoutConsumingKeys(t *testing.T) {
	conn, service := syncInboxTestDB(t)
	enqueueSyncKeys(t, conn, "@logout:test", "A", 1)
	require.NoError(t, conn.Create(&MatrixUserSession{UserID: "@logout:test", DeviceID: "A", AccessToken: "ack-logout-fixture"}).Error)
	_, _, err := service.getToDeviceMessages("@logout:test", "A", "")
	require.NoError(t, err)
	require.Nil(t, (&AuthService{}).Logout("ack-logout-fixture"))
	var count int64
	require.NoError(t, conn.Model(&MatrixSyncToDeviceBatch{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, conn.Model(&MatrixToDeviceMessage{}).Count(&count).Error)
	require.Equal(t, int64(1), count, "logout is not an acknowledgement of unseen payload")
}

func TestSyncNextBatchAcknowledgesOnlyDeliveredDeviceBatch(t *testing.T) {
	hs := createTestHomeServer(t)
	conn := database.GetDB()
	const user = "@sync-ack-integration:test.local"
	enqueueSyncKeys(t, conn, user, "D", 1)
	req := &SyncRequest{Since: "s1", SetPresence: "offline"}
	first, matrixErr := hs.Sync.Sync(user, "D", req)
	require.Nil(t, matrixErr)
	require.Len(t, first.ToDevice.Events, 1)
	batch := toDeviceAck(first.NextBatch)
	require.NotEmpty(t, batch)
	require.Positive(t, hs.Sync.parseSyncToken(first.NextBatch))
	retry, matrixErr := hs.Sync.Sync(user, "D", req)
	require.Nil(t, matrixErr)
	require.Equal(t, batch, toDeviceAck(retry.NextBatch))
	require.Equal(t, first.ToDevice, retry.ToDevice)
	acked, matrixErr := hs.Sync.Sync(user, "D", &SyncRequest{Since: first.NextBatch, SetPresence: "offline"})
	require.Nil(t, matrixErr)
	require.Empty(t, acked.ToDevice.Events)
	require.Empty(t, toDeviceAck(acked.NextBatch))
}

func TestToDeviceCanceledBatchTransactionRetainsKeys(t *testing.T) {
	conn, service := syncInboxTestDB(t)
	enqueueSyncKeys(t, conn, "@rollback:test", "D", 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	require.NoError(t, conn.Callback().Update().Before("gorm:update").Register("test:cancel-delivery", func(tx *gorm.DB) {
		if tx.Statement.Table == "matrix_sync_to_device_batches" {
			cancel()
		}
	}))
	deferred := func() { _ = conn.Callback().Update().Remove("test:cancel-delivery") }
	t.Cleanup(deferred)
	canceled := &SyncService{connection: conn.WithContext(ctx)}
	_, _, err := canceled.getToDeviceMessages("@rollback:test", "D", "")
	require.ErrorIs(t, err, context.Canceled)
	deferred()
	// A file-backed fixture survives SQLite discarding a canceled connection.
	// The regular
	// cancellation test already covers a completed pending batch. This case
	// specifically requires the DB transaction to retain the original payload.
	messages, batch, err := service.getToDeviceMessages("@rollback:test", "D", "")
	require.NoError(t, err)
	require.Len(t, messages.Events, 1)
	require.NotEmpty(t, batch)
}
