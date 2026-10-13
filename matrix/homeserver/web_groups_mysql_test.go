package homeserver

import (
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/db/database"
	"github.com/stretchr/testify/require"
	mysqlgorm "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// COMMUNITY_GROUPS_MYSQL_DSN selects an explicitly disposable MySQL server.
// Each invocation creates and drops its own randomly named database, and never
// writes the database named in the input DSN. Credentials are never logged.
func TestWebGroupMySQLStreamTransactions(t *testing.T) {
	dsn := os.Getenv("COMMUNITY_GROUPS_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set COMMUNITY_GROUPS_MYSQL_DSN to run real row-lock and commit-order checks")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	require.NoError(t, err)
	cfg.DBName = ""
	admin, err := sql.Open("mysql", cfg.FormatDSN())
	require.NoError(t, err)
	t.Cleanup(func() { admin.Close() })
	name := "community_groups_stream_qa_" + uuid.NewString()[:8]
	_, err = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP DATABASE `" + name + "`")
		if err != nil {
			t.Errorf("could not remove isolated MySQL test database: %v", err)
		}
	})
	cfg.DBName = name
	cfg.ParseTime = true
	conn, err := gorm.Open(mysqlgorm.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(24)
	t.Cleanup(func() { sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&MatrixRoom{}, &MatrixEvent{}, &MatrixRoomState{}, &MatrixRoomMember{}, &MatrixAccountData{}, &MatrixRoomAlias{}, &BridgeFederationDelivery{}, &MatrixUserSession{}))
	original := database.DB
	database.DB = conn
	t.Cleanup(func() { database.DB = original })
	hs := fullHS("test.local")
	hs.Push = nil
	hs.SyncNotifier = nil
	t.Cleanup(resetGlobalHS(hs))
	room := createRoomWithUser(t, hs, "@alice:test.local")
	other := createRoomWithUser(t, hs, "@alice:test.local")
	var before MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", room).Take(&before).Error)
	err = conn.Transaction(func(tx *gorm.DB) error {
		event := MatrixEvent{RoomID: room, Sender: "@alice:test.local", EventType: EventTypeRoomMessage, Content: map[string]any{"msgtype": "m.text", "body": "rollback"}}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}
		return errors.New("deliberate rollback")
	})
	require.Error(t, err)
	var after MatrixRoom
	require.NoError(t, conn.Where("room_id = ?", room).Take(&after).Error)
	require.Equal(t, before.WebStreamPosition, after.WebStreamPosition)
	started, release := make(chan struct{}), make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- conn.Transaction(func(tx *gorm.DB) error {
			event := MatrixEvent{RoomID: room, Sender: "@alice:test.local", EventType: EventTypeRoomMessage, Content: map[string]any{"msgtype": "m.text", "body": "first held transaction"}}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
			close(started)
			<-release
			return nil
		})
	}()
	select {
	case <-started:
	case err := <-firstDone:
		t.Fatalf("first transaction failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("first transaction did not start")
	}
	type result struct {
		event   *MatrixEvent
		failure *MatrixError
	}
	secondDone := make(chan result, 1)
	go func() {
		event, failure := webGroupPersist(hs, conn, room, "@alice:test.local", "second", webGroupHash("second"), "m.room.message", map[string]any{"msgtype": "m.text", "body": "second transaction"}, "", "")
		secondDone <- result{event, failure}
	}()
	select {
	case <-secondDone:
		t.Fatal("same room write committed before the prior transaction")
	case <-time.After(100 * time.Millisecond):
	}
	// Another room must remain writable while this room's transaction is held.
	independentDone := make(chan result, 1)
	go func() {
		event, failure := webGroupPersist(hs, conn, other, "@alice:test.local", "independent", webGroupHash("independent"), "m.room.message", map[string]any{"msgtype": "m.text", "body": "independent room"}, "", "")
		independentDone <- result{event, failure}
	}()
	select {
	case result := <-independentDone:
		require.Nil(t, result.failure)
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("unrelated room was blocked by a global lock")
	}
	close(release)
	require.NoError(t, <-firstDone)
	second := <-secondDone
	require.Nil(t, second.failure)
	var events []MatrixEvent
	require.NoError(t, conn.Where("room_id = ? AND event_type = ?", room, EventTypeRoomMessage).Order("stream_position").Find(&events).Error)
	require.Len(t, events, 2)
	require.Less(t, events[0].StreamPosition, events[1].StreamPosition)
	require.Equal(t, second.event.EventID, events[1].EventID)
	// Concurrent identical transactions commit exactly one event and one position.
	var workers sync.WaitGroup
	results := make(chan result, 10)
	for range 10 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			event, failure := webGroupPersist(hs, conn, room, "@alice:test.local", "same-transaction", webGroupHash("same"), "m.room.message", map[string]any{"msgtype": "m.text", "body": "same transaction"}, "", "")
			results <- result{event, failure}
		}()
	}
	workers.Wait()
	close(results)
	ids := map[string]bool{}
	for result := range results {
		require.Nil(t, result.failure)
		require.NotNil(t, result.event)
		ids[result.event.EventID] = true
	}
	require.Len(t, ids, 1)
	var count int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND web_content_hash = ?", room, webGroupHash("same")).Count(&count).Error)
	require.EqualValues(t, 1, count)
	// Committing encryption under the shared lock must deny an already waiting
	// plaintext send after it gets the lock, not just future HTTP requests.
	encryptedStarted, encryptRelease := make(chan struct{}), make(chan struct{})
	encryptedDone := make(chan error, 1)
	go func() {
		encryptedDone <- conn.Transaction(func(tx *gorm.DB) error {
			key := ""
			event := MatrixEvent{RoomID: room, Sender: "@alice:test.local", EventType: EventTypeRoomEncryption, StateKey: &key, Content: map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}}
			if err := tx.Create(&event).Error; err != nil {
				return err
			}
			hs.Room.updateRoomState(tx, room, &event)
			close(encryptedStarted)
			<-encryptRelease
			return nil
		})
	}()
	select {
	case <-encryptedStarted:
	case err := <-encryptedDone:
		t.Fatalf("encryption transaction failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("encryption did not start")
	}
	plaintextDone := make(chan result, 1)
	go func() {
		event, failure := webGroupPersist(hs, conn, room, "@alice:test.local", "encryption-race", webGroupHash("blocked"), "m.room.message", map[string]any{"msgtype": "m.text", "body": "must not be committed"}, "", "")
		plaintextDone <- result{event, failure}
	}()
	select {
	case <-plaintextDone:
		t.Fatal("plaintext send did not wait for encryption transaction")
	case <-time.After(100 * time.Millisecond):
	}
	close(encryptRelease)
	require.NoError(t, <-encryptedDone)
	blocked := <-plaintextDone
	require.NotNil(t, blocked.failure)
	require.Equal(t, "M_GROUP_ENCRYPTED", blocked.failure.ErrCode)
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND web_content_hash = ?", room, webGroupHash("blocked")).Count(&count).Error)
	require.Zero(t, count)
}
