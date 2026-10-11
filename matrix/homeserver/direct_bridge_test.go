package homeserver

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"fmt"
	driver "github.com/go-sql-driver/mysql"
	"github.com/kasuganosora/community/storage"
	gmysql "gorm.io/driver/mysql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func directBridgeDB(t *testing.T) (*HomeServer, *gorm.DB) {
	t.Helper()
	var dialect gorm.Dialector = sqlite.Open(filepath.Join(t.TempDir(), "bridge.db"))
	if strings.Contains(t.Name(), "MySQL") {
		dsn := os.Getenv("COMMUNITY_MIGRATION_TEST_MYSQL_DSN")
		if dsn == "" {
			t.Skip("requires isolated QA MySQL credentials")
		}
		cfg, err := driver.ParseDSN(dsn)
		require.NoError(t, err)
		cfg.DBName = ""
		admin, err := gorm.Open(gmysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Discard})
		require.NoError(t, err)
		name := fmt.Sprintf("qa_matrix_dm_%d", time.Now().UnixNano())
		require.NoError(t, admin.Exec("CREATE DATABASE `"+name+"`").Error)
		t.Cleanup(func() { admin.Exec("DROP DATABASE `" + name + "`"); raw, _ := admin.DB(); raw.Close() })
		cfg.DBName = name
		dialect = gmysql.Open(cfg.FormatDSN())
	}
	conn, e := gorm.Open(dialect, &gorm.Config{Logger: logger.Discard, DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, e)
	old := database.DB
	database.DB = conn
	t.Cleanup(func() {
		if old != nil {
			database.DB = old
		}
		raw, _ := conn.DB()
		raw.Close()
	})
	require.NoError(t, conn.AutoMigrate(Models()...))
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.Status{}, &models.StatusContent{}, &models.MediaAttachment{}, &models.StatusToAccount{}, &models.Block{}, &models.DomainBlock{}))
	pub, priv, e := ed25519.GenerateKey(nil)
	require.NoError(t, e)
	hs := &HomeServer{Config: &HSConfig{ServerName: "test.local"}, privateKey: priv, publicKey: pub, keyID: "ed25519:test"}
	hs.Room = NewRoomService(hs)
	hs.Event = NewEventService(hs)
	hs.Sync = NewSyncService(hs)
	hs.Auth = NewAuthService(hs)
	prior := GlobalHS
	GlobalHS = hs
	t.Cleanup(func() { GlobalHS = prior })
	domain := "remote.example"
	for _, a := range []models.Account{{Id: 1, Username: "remote", Domain: &domain}, {Id: 2, Username: "alice"}, {Id: 3, Username: "bob"}, {Id: 4, Username: "outsider"}} {
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Extra").Create(&a).Error)
	}
	return hs, conn
}
func seedDirectBridge(t *testing.T, conn *gorm.DB, id uint64, recipients ...uint64) {
	t.Helper()
	s := models.Status{Id: id, AccountId: 1, Visibility: models.StatusVisibilityDirect}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Content").Create(&s).Error)
	require.NoError(t, conn.Create(&models.StatusContent{StatusId: id, Content: "<p>private message</p>"}).Error)
	for _, id2 := range recipients {
		require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: id, AccountId: id2}).Error)
	}
	require.NoError(t, models.QueueMatrixDirectMessage(conn, &s))
}
func TestDirectBridgeExistingAndLaterMatrixLoginPrivateAndIdempotent(t *testing.T) {
	hs, conn := directBridgeDB(t)
	ctx := context.Background()
	aliceID := uint64(2)
	require.NoError(t, conn.Create(&MatrixUserSession{UserID: "@alice:test.local", LocalAccountID: &aliceID, DeviceID: "ALREADY_LOGGED_IN", AccessToken: "test-private-token"}).Error)
	seedDirectBridge(t, conn, 11, 2, 3)
	require.NoError(t, hs.ProcessDirectMessage(ctx, 11))
	var job models.MatrixDMDelivery
	require.NoError(t, conn.First(&job, "status_id = ?", 11).Error)
	require.Equal(t, "delivered", job.State)
	for _, user := range []string{"@alice:test.local", "@bob:test.local"} {
		membership, me := hs.Room.GetUserMembership(job.RoomID, user)
		require.Nil(t, me)
		require.Equal(t, MembershipJoin, membership)
	}
	_, me := hs.Room.JoinRoom("@outsider:test.local", job.RoomID, &JoinRoomRequest{})
	require.NotNil(t, me)
	var create MatrixEvent
	require.NoError(t, conn.Where("room_id = ? AND event_type = ?", job.RoomID, EventTypeRoomCreate).First(&create).Error)
	require.Equal(t, false, create.Content["m.federate"])
	var msg MatrixEvent
	require.NoError(t, conn.First(&msg, "event_id = ?", job.EventID).Error)
	require.True(t, strings.HasPrefix(msg.Sender, "@_ap_"))
	require.Contains(t, msg.Content["body"], "private message")
	// Simulate process failure after sending but before acknowledging the durable job.
	require.NoError(t, conn.Model(&job).Updates(map[string]any{"state": "pending", "next_attempt_at": time.Now().Add(-time.Second)}).Error)
	restarted := *hs
	require.NoError(t, restarted.ProcessDirectMessage(ctx, 11))
	require.NoError(t, restarted.ProcessDirectMessage(ctx, 11))
	var count int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", job.RoomID, EventTypeRoomMessage).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var bob models.Account
	require.NoError(t, conn.First(&bob, 3).Error)
	login, me := hs.Auth.createSession(bob, nil, nil, false)
	require.Nil(t, me)
	require.Equal(t, "@bob:test.local", login.UserID)
	for _, user := range []string{"@alice:test.local", login.UserID} {
		sync, me := hs.Sync.Sync(user, "ALREADY_LOGGED_IN", &SyncRequest{})
		require.Nil(t, me)
		require.NotNil(t, sync.Rooms.Join[job.RoomID])
		found := false
		for _, event := range sync.Rooms.Join[job.RoomID].Timeline.Events {
			if event.EventID == job.EventID {
				found = true
			}
		}
		require.True(t, found, "authenticated account must receive the bridged DM in sync")
	}
	_, me = hs.Event.GetRoomMessages(job.RoomID, "@outsider:test.local", &GetRoomMessagesRequest{Dir: "b"})
	require.NotNil(t, me)

	// Different explicit audience must not reuse the group's room.
	seedDirectBridge(t, conn, 12, 2)
	require.NoError(t, hs.ProcessDirectMessage(ctx, 12))
	var private models.MatrixDMDelivery
	require.NoError(t, conn.First(&private, "status_id = ?", 12).Error)
	require.NotEqual(t, job.RoomID, private.RoomID)
	member, _ := hs.Room.GetUserMembership(private.RoomID, "@bob:test.local")
	require.NotEqual(t, MembershipJoin, member)
}
func TestDirectBridgeRejectsNonDirectBlockedAndRolledBackMessages(t *testing.T) {
	hs, conn := directBridgeDB(t)
	seedDirectBridge(t, conn, 21, 2)
	require.NoError(t, conn.Create(&models.Block{AccountId: 2, TargetAccountId: 1}).Error)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 21))
	var job models.MatrixDMDelivery
	require.NoError(t, conn.First(&job, "status_id = ?", 21).Error)
	require.Equal(t, "skipped", job.State)
	tx := conn.Begin()
	require.NoError(t, models.QueueMatrixDirectMessage(tx, &models.Status{Id: 99, Visibility: models.StatusVisibilityDirect}))
	tx.Rollback()
	var count int64
	require.NoError(t, conn.Model(&models.MatrixDMDelivery{}).Where("status_id = ?", 99).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, models.QueueMatrixDirectMessage(conn, &models.Status{Id: 100, Visibility: models.StatusVisibilityPublic}))
	require.NoError(t, conn.Model(&models.MatrixDMDelivery{}).Where("status_id = ?", 100).Count(&count).Error)
	require.Zero(t, count)
}

func TestDirectBridgeAttachmentBytesAndRetry(t *testing.T) {
	hs, conn := directBridgeDB(t)
	ctx := context.Background()
	restore := storage.SetDefault(&storage.LocalBackend{Root: t.TempDir()})
	defer restore()
	seedDirectBridge(t, conn, 31, 2)
	data := []byte("private attachment bytes\n")
	attachment := models.MediaAttachment{Id: 51, AccountId: 1, StatusId: new(uint64), FileName: "private.txt", FilePath: "qa/private.txt", ContentType: "text/plain", FileSize: int64(len(data)), Type: models.MediaTypeUnknown}
	*attachment.StatusId = 31
	require.NoError(t, storage.Default().Put(ctx, attachment.StorageKey(), bytes.NewReader(data), int64(len(data)), "text/plain"))
	require.NoError(t, conn.Create(&attachment).Error)
	require.NoError(t, hs.ProcessDirectMessage(ctx, 31))
	var job models.MatrixDMDelivery
	require.NoError(t, conn.First(&job, "status_id = ?", 31).Error)
	var media MatrixMedia
	require.NoError(t, conn.First(&media).Error)
	require.Equal(t, "private.txt", media.FileName)
	require.Equal(t, "text/plain", media.ContentType)
	r, _, e := storage.Open(ctx, media.FilePath)
	require.NoError(t, e)
	actual, e := io.ReadAll(r)
	r.Close()
	require.NoError(t, e)
	require.Equal(t, data, actual)
	require.NoError(t, conn.Model(&job).Updates(map[string]any{"state": "pending", "next_attempt_at": time.Now().Add(-time.Second)}).Error)
	require.NoError(t, hs.ProcessDirectMessage(ctx, 31))
	var events []MatrixEvent
	require.NoError(t, conn.Where("room_id = ? AND event_type = ?", job.RoomID, EventTypeRoomMessage).Find(&events).Error)
	require.Len(t, events, 2)
	found := false
	for _, e := range events {
		if e.Content["msgtype"] == "m.file" {
			found = true
			require.Equal(t, "mxc://test.local/"+media.MediaID, e.Content["url"])
			require.Equal(t, "private.txt", e.Content["filename"])
		}
	}
	require.True(t, found)
	var count int64
	require.NoError(t, conn.Model(&MatrixMedia{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestDirectBridgeConcurrentLeaseDoesNotDuplicate(t *testing.T) {
	hs, conn := directBridgeDB(t)
	raw, err := conn.DB()
	require.NoError(t, err)
	if conn.Dialector.Name() == "mysql" {
		raw.SetMaxOpenConns(8)
	} else {
		raw.SetMaxOpenConns(1)
	}
	seedDirectBridge(t, conn, 41, 2)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- hs.ProcessDirectMessage(context.Background(), 41) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var job models.MatrixDMDelivery
	require.NoError(t, conn.First(&job, "status_id = ?", 41).Error)
	require.Equal(t, "delivered", job.State)
	var count int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", job.RoomID, EventTypeRoomMessage).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestDirectBridgeMySQLConcurrentLease(t *testing.T) {
	TestDirectBridgeConcurrentLeaseDoesNotDuplicate(t)
}

func TestDirectBridgeReinvitesLeaveAndFailsClosedOnAlteredRoom(t *testing.T) {
	hs, conn := directBridgeDB(t)
	ctx := context.Background()
	seedDirectBridge(t, conn, 61, 2, 3)
	require.NoError(t, hs.ProcessDirectMessage(ctx, 61))
	var job models.MatrixDMDelivery
	require.NoError(t, conn.First(&job, "status_id = ?", 61).Error)
	require.Nil(t, hs.Room.LeaveRoom("@bob:test.local", job.RoomID, &LeaveRoomRequest{}))
	seedDirectBridge(t, conn, 62, 2, 3)
	require.NoError(t, hs.ProcessDirectMessage(ctx, 62))
	membership, e := hs.Room.GetUserMembership(job.RoomID, "@bob:test.local")
	require.NoError(t, e)
	require.Equal(t, MembershipInvite, membership)
	// Only the inaccessible bridge creator can invite or alter encryption state.
	_, me := hs.Event.SendRoomEvent(job.RoomID, "@alice:test.local", string(EventTypeRoomEncryption), nil, map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}, new(""))
	require.NotNil(t, me)
	require.NoError(t, conn.Create(&MatrixRoomState{RoomID: job.RoomID, EventType: EventTypeRoomEncryption, StateKey: "", Content: map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}}).Error)
	seedDirectBridge(t, conn, 63, 2, 3)
	require.Error(t, hs.ProcessDirectMessage(ctx, 63))
	var last models.MatrixDMDelivery
	require.NoError(t, conn.First(&last, "status_id = ?", 63).Error)
	require.Equal(t, "pending", last.State)
	var count int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("room_id = ? AND event_type = ?", job.RoomID, EventTypeRoomMessage).Count(&count).Error)
	require.EqualValues(t, 2, count)
	var sender models.Account
	require.NoError(t, conn.First(&sender, 1).Error)
	ghost := hs.bridgeAccountID(sender)
	local, _, ok := ParseMatrixUserID(ghost)
	require.True(t, ok)
	_, me = hs.Auth.Register(&RegisterRequest{Username: local, Password: "never-register-a-ghost"}, false)
	require.NotNil(t, me)
	require.Equal(t, "M_INVALID_USERNAME", me.ErrCode)
}

func TestDirectBridgeAttachmentRejectsPrivateAndCredentialURLs(t *testing.T) {
	for _, target := range []string{"https://127.0.0.1/private", "https://169.254.169.254/latest/meta-data/", "http://remote.example/file", "https://user:secret@remote.example/file"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, _, err := fetchDirectAttachment(ctx, target)
		cancel()
		require.Error(t, err)
	}
}

func TestDirectBridgeCurrentDomainAndGlobalBlockPolicy(t *testing.T) {
	cases := []struct {
		name   string
		block  *models.Block
		domain *models.DomainBlock
	}{
		{name: "recipient domain block", block: &models.Block{AccountId: 2, Type: models.BlockTypeDomain, TargetDomain: "REMOTE.EXAMPLE"}},
		{name: "global account block", block: &models.Block{AccountId: 0, Type: models.BlockTypeAccount, TargetAccountId: 1}},
		{name: "global domain block", block: &models.Block{AccountId: 0, Type: models.BlockTypeDomain, TargetDomain: "remote.example"}},
		{name: "suspended domain", domain: &models.DomainBlock{Domain: "remote.example", Severity: "suspend"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			hs, conn := directBridgeDB(t)
			seedDirectBridge(t, conn, 900, 2)
			// Policy changed after acceptance but before asynchronous bridge delivery.
			if tt.block != nil {
				require.NoError(t, conn.Create(tt.block).Error)
			}
			if tt.domain != nil {
				require.NoError(t, conn.Create(tt.domain).Error)
			}
			require.NoError(t, hs.ProcessDirectMessage(context.Background(), 900))
			var job models.MatrixDMDelivery
			require.NoError(t, conn.First(&job, "status_id = ?", 900).Error)
			require.Equal(t, "skipped", job.State)
			var n int64
			require.NoError(t, conn.Model(&MatrixEvent{}).Count(&n).Error)
			require.Zero(t, n, "blocked historical DM must not create a Matrix room or event")
		})
	}
}

func TestDirectBridgeDeletedSenderSkippedButDatabaseFailureRetried(t *testing.T) {
	t.Run("deleted sender", func(t *testing.T) {
		hs, conn := directBridgeDB(t)
		seedDirectBridge(t, conn, 901, 2)
		require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 1).UpdateColumn("deleted_at", time.Now()).Error)
		require.NoError(t, hs.ProcessDirectMessage(context.Background(), 901))
		var job models.MatrixDMDelivery
		require.NoError(t, conn.First(&job, "status_id = ?", 901).Error)
		require.Equal(t, "skipped", job.State)
		var n int64
		require.NoError(t, conn.Model(&MatrixEvent{}).Count(&n).Error)
		require.Zero(t, n)
	})
	t.Run("database failure", func(t *testing.T) {
		hs, conn := directBridgeDB(t)
		seedDirectBridge(t, conn, 902, 2)
		require.NoError(t, conn.Migrator().DropTable(&models.Account{}))
		require.Error(t, hs.ProcessDirectMessage(context.Background(), 902))
		var job models.MatrixDMDelivery
		require.NoError(t, conn.First(&job, "status_id = ?", 902).Error)
		require.Equal(t, "pending", job.State)
		require.Empty(t, job.LeaseToken)
		require.Equal(t, 1, job.Attempts)
		require.True(t, job.NextAttemptAt.After(time.Now()))
	})
}

func TestDirectBridgeRecipientDomainBlockNarrowsExistingGroup(t *testing.T) {
	hs, conn := directBridgeDB(t)
	seedDirectBridge(t, conn, 903, 2, 3)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 903))
	var original models.MatrixDMDelivery
	require.NoError(t, conn.First(&original, "status_id = ?", 903).Error)
	require.NoError(t, conn.Create(&models.Block{AccountId: 2, Type: models.BlockTypeDomain, TargetDomain: "remote.example"}).Error)
	seedDirectBridge(t, conn, 904, 2, 3)
	require.NoError(t, hs.ProcessDirectMessage(context.Background(), 904))
	var next models.MatrixDMDelivery
	require.NoError(t, conn.First(&next, "status_id = ?", 904).Error)
	require.Equal(t, "delivered", next.State)
	require.NotEqual(t, original.RoomID, next.RoomID)
	var alice int64
	require.NoError(t, conn.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ? AND membership = ?", next.RoomID, "@alice:test.local", MembershipJoin).Count(&alice).Error)
	require.Zero(t, alice)
	membership, me := hs.Room.GetUserMembership(next.RoomID, "@bob:test.local")
	require.Nil(t, me)
	require.Equal(t, MembershipJoin, membership)
	var misplaced int64
	require.NoError(t, conn.Model(&MatrixEvent{}).Where("event_id = ? AND room_id = ?", next.EventID, original.RoomID).Count(&misplaced).Error)
	require.Zero(t, misplaced, "new content must not enter the room still accessible to the blocking recipient")
}
