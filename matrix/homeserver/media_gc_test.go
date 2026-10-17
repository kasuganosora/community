package homeserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func mediaGCFixture(t *testing.T) (*gorm.DB, *storagetest.Memory, time.Time) {
	t.Helper()
	setupMinimalTestDB(t)
	conn := db.GetDB()
	require.NoError(t, conn.AutoMigrate(&MatrixThumbnail{}, &WebGroupUpload{}, &AsyncMediaInfo{}, &models.Account{}, &models.MediaAttachment{}))
	store := storagetest.NewMemory()
	t.Cleanup(storage.SetDefault(store))
	return conn, store, time.Now().UTC().Truncate(time.Second)
}
func seedGCMedia(t *testing.T, conn *gorm.DB, store *storagetest.Memory, now time.Time, id, origin string) MatrixMedia {
	t.Helper()
	m := MatrixMedia{MediaID: id, ServerName: "test.local", FilePath: "matrix/" + origin + "/" + id + ".png", FileSize: 4, ContentType: "image/png", Origin: origin, GCEligible: true, CreatedAt: now.Add(-48 * time.Hour), UploaderID: "@alice:test.local"}
	require.NoError(t, conn.Create(&m).Error)
	require.NoError(t, store.Put(context.Background(), m.FilePath, strings.NewReader("data"), 4, m.ContentType))
	return m
}
func gcRun(t *testing.T, conn *gorm.DB, now time.Time, apply bool, origin string, limit int) MediaGCResult {
	t.Helper()
	result, err := CollectMedia(context.Background(), conn, now, MediaGCOptions{Apply: apply, Origin: origin, Limit: limit, Grace: 24 * time.Hour})
	require.NoError(t, err)
	return result
}
func gcEvent(t *testing.T, conn *gorm.DB, id string, m MatrixMedia) MatrixEvent {
	t.Helper()
	e := MatrixEvent{EventID: id, RoomID: "!room:test.local", Sender: m.UploaderID, EventType: EventTypeRoomMessage, Content: map[string]any{"url": "mxc://" + m.ServerName + "/" + m.MediaID}}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&e).Error)
	return e
}

func TestMediaGCRedactionSharedReferencesAndGrace(t *testing.T) {
	conn, store, now := mediaGCFixture(t)
	m := seedGCMedia(t, conn, store, now, "shared", "local")
	first := gcEvent(t, conn, "$first", m)
	second := gcEvent(t, conn, "$second", m)
	require.NoError(t, applyStoredRedactionTx(conn, first.RoomID, first.EventID))
	require.Equal(t, 1, gcRun(t, conn, now, true, "", 32).Retained)
	_, err := store.Stat(context.Background(), m.FilePath)
	require.NoError(t, err)
	require.NoError(t, applyStoredRedactionTx(conn, second.RoomID, second.EventID))
	require.Equal(t, 1, gcRun(t, conn, now.Add(time.Hour), true, "", 32).Waiting)
	require.Zero(t, gcRun(t, conn, now.Add(24*time.Hour), true, "", 32).Deleted)
	require.Equal(t, 1, gcRun(t, conn, now.Add(25*time.Hour), true, "", 32).Deleted)
	_, err = store.Stat(context.Background(), m.FilePath)
	require.ErrorIs(t, err, storage.ErrNotFound)
	require.Error(t, pinMatrixMedia(conn, "mxc://test.local/shared"), "a deleted MXC cannot be resurrected")
}

func TestMediaGCRetainsProfilesSharedObjectsAndOpaqueUploads(t *testing.T) {
	conn, store, now := mediaGCFixture(t)
	avatar := seedGCMedia(t, conn, store, now, "avatar", "remote")
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Extra").Create(&models.Account{Id: 6, Username: "alice", Avatar: "mxc://test.local/" + avatar.MediaID}).Error)
	shared := seedGCMedia(t, conn, store, now, "community", "local")
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.MediaAttachment{Id: 8, FilePath: shared.FilePath}).Error)
	native := seedGCMedia(t, conn, store, now, "native", "local")
	require.NoError(t, conn.Model(&native).Update("gc_eligible", false).Error)
	result := gcRun(t, conn, now, true, "", 32)
	require.Equal(t, 2, result.Retained)
	require.Equal(t, 2, result.Scanned)
	encrypted := MatrixEvent{EventID: "$encrypted", RoomID: "!encrypted:test.local", Sender: "@alice:test.local", EventType: "m.room.encrypted", Content: map[string]any{"ciphertext": "opaque"}}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&encrypted).Error)
	seedGCMedia(t, conn, store, now, "opaque-reuse", "local")
	require.Equal(t, 1, gcRun(t, conn, now, true, "", 32).Retained)
}

func TestMediaGCDeletionFailureRetriesWithMetadataAndThumbnails(t *testing.T) {
	conn, store, now := mediaGCFixture(t)
	m := seedGCMedia(t, conn, store, now, "retry", "remote")
	thumbKey := "matrix/remote/thumb.png"
	require.NoError(t, store.Put(context.Background(), thumbKey, strings.NewReader("thumb"), 5, "image/png"))
	require.NoError(t, conn.Create(&MatrixThumbnail{MediaID: m.MediaID, FilePath: "thumb.png"}).Error)
	gcRun(t, conn, now, true, "", 32)
	store.Fail = errors.New("temporary storage failure")
	_, err := CollectMedia(context.Background(), conn, now.Add(24*time.Hour), MediaGCOptions{Apply: true, Grace: 24 * time.Hour})
	require.Error(t, err)
	var failed MatrixMedia
	require.NoError(t, conn.First(&failed, m.ID).Error)
	require.Equal(t, "deleting", failed.GCState)
	require.Equal(t, m.FilePath, failed.FilePath)
	require.Error(t, pinMatrixMedia(conn, "mxc://test.local/"+m.MediaID))
	store.Fail = nil
	require.Equal(t, 1, gcRun(t, conn, now.Add(25*time.Hour), true, "", 32).Deleted)
	_, err = store.Stat(context.Background(), thumbKey)
	require.ErrorIs(t, err, storage.ErrNotFound)
	require.NoError(t, conn.First(&failed, m.ID).Error)
	require.Equal(t, "deleted", failed.GCState)
	require.Empty(t, failed.FilePath)
}

func TestMediaGCDryRunOriginFilterAndBatchProgress(t *testing.T) {
	conn, store, now := mediaGCFixture(t)
	local := seedGCMedia(t, conn, store, now, "local", "local")
	remote := seedGCMedia(t, conn, store, now, "remote", "remote")
	require.Equal(t, 1, gcRun(t, conn, now, false, "remote", 32).Waiting)
	var row MatrixMedia
	require.NoError(t, conn.First(&row, remote.ID).Error)
	require.Nil(t, row.GCUnreferencedAt)
	gcEvent(t, conn, "$keep-local", local)
	require.Equal(t, 1, gcRun(t, conn, now, true, "", 1).Retained)
	require.Equal(t, 1, gcRun(t, conn, now, true, "", 1).Waiting, "referenced rows must not starve later candidates")
	require.Equal(t, 1, gcRun(t, conn, now.Add(24*time.Hour), false, "remote", 32).Deleted)
	_, err := store.Stat(context.Background(), remote.FilePath)
	require.NoError(t, err, "dry run must not delete bytes")
	require.Equal(t, 1, gcRun(t, conn, now.Add(24*time.Hour), true, "remote", 32).Deleted)
	_, err = store.Stat(context.Background(), local.FilePath)
	require.NoError(t, err)
}

func TestMediaGCPublicationCancelsCandidateAndQueryFailureRetainsBytes(t *testing.T) {
	conn, store, now := mediaGCFixture(t)
	m := seedGCMedia(t, conn, store, now, "racing", "local")
	gcRun(t, conn, now, true, "", 32)
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		if err := pinMatrixMedia(tx, map[string]any{"formatted_body": "<img src=\"mxc://test.local/racing\">"}); err != nil {
			return err
		}
		gcEvent(t, tx, "$published", m)
		return nil
	}))
	require.Equal(t, 1, gcRun(t, conn, now.Add(24*time.Hour), true, "", 32).Retained)
	seedGCMedia(t, conn, store, now, "query-error", "local")
	require.NoError(t, conn.Migrator().DropTable(&MatrixRoomState{}))
	_, err := CollectMedia(context.Background(), conn, now, MediaGCOptions{Apply: true})
	require.Error(t, err)
	_, err = store.Stat(context.Background(), "matrix/local/query-error.png")
	require.NoError(t, err)
}

func TestMediaGCAsyncReservationsRespectActiveClaims(t *testing.T) {
	conn, _, now := mediaGCFixture(t)
	expired, active := now.Add(-time.Hour), now.Add(time.Hour)
	rows := []AsyncMediaInfo{
		{MediaID: "pending", Status: "pending", ExpiresAt: &expired},
		{MediaID: "failed", Status: "failed", ExpiresAt: &expired},
		{MediaID: "stale", Status: "processing", ExpiresAt: &expired, ClaimExpiresAt: &expired},
		{MediaID: "active", Status: "processing", ExpiresAt: &expired, ClaimExpiresAt: &active},
		{MediaID: "done", Status: "done", ExpiresAt: &expired},
	}
	require.NoError(t, conn.Create(&rows).Error)
	require.NoError(t, cleanupAsyncMedia(context.Background(), conn, now))
	var remaining []AsyncMediaInfo
	require.NoError(t, conn.Find(&remaining).Error)
	require.Len(t, remaining, 1)
	require.Equal(t, "active", remaining[0].MediaID)
}

type gatedMediaDelete struct {
	storage.Backend
	started chan struct{}
	release chan struct{}
}

func (b *gatedMediaDelete) Delete(ctx context.Context, key string) error {
	close(b.started)
	select {
	case <-b.release:
		return b.Backend.Delete(ctx, key)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestMediaGCConcurrentPublicationCannotResurrectDeletedFile(t *testing.T) {
	conn, store, now := mediaGCFixture(t)
	m := seedGCMedia(t, conn, store, now, "concurrent", "local")
	gcRun(t, conn, now, true, "", 32)
	gate := &gatedMediaDelete{Backend: store, started: make(chan struct{}), release: make(chan struct{})}
	restore := storage.SetDefault(gate)
	defer restore()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	collected := make(chan error, 1)
	go func() {
		_, err := CollectMedia(ctx, conn, now.Add(24*time.Hour), MediaGCOptions{Apply: true, Grace: 24 * time.Hour})
		collected <- err
	}()
	select {
	case <-gate.started:
	case <-ctx.Done():
		t.Fatal("collector did not reach storage")
	}
	published := make(chan error, 1)
	go func() {
		published <- conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return pinMatrixMedia(tx, "mxc://test.local/"+m.MediaID) })
	}()
	close(gate.release)
	require.NoError(t, <-collected)
	require.Error(t, <-published)
	require.NoError(t, pinMatrixMediaReferences(conn, "mxc://test.local/"+m.MediaID, false), "valid federated history may retain an expired MXC")
}

func TestMediaGCUsedLegacyLeasePreservesProvenance(t *testing.T) {
	conn, store, now := mediaGCFixture(t)
	m := seedGCMedia(t, conn, store, now, "legacy-web", "local")
	require.NoError(t, conn.Model(&m).Updates(map[string]any{"gc_eligible": false, "origin": ""}).Error)
	require.NoError(t, conn.Create(&WebGroupUpload{ID: "old-lease", RoomID: "!room:test.local", UserID: m.UploaderID, MediaID: m.MediaID, ServerName: m.ServerName, UsedEventID: "$used", ExpiresAt: now.Add(-time.Hour)}).Error)
	require.NoError(t, cleanupWebGroupUploads(context.Background(), nil, now))
	require.NoError(t, conn.First(&m, m.ID).Error)
	require.True(t, m.GCEligible)
	require.Empty(t, m.Origin, "do not guess the directory/origin of old objects")
	require.Equal(t, 1, gcRun(t, conn, now, true, "", 32).Waiting)
}

func TestMediaGCProtectsSharedThumbnailBytes(t *testing.T) {
	conn, store, now := mediaGCFixture(t)
	m := seedGCMedia(t, conn, store, now, "shared-thumb", "local")
	sharedKey := "matrix/local/shared-thumbnail.png"
	require.NoError(t, store.Put(context.Background(), sharedKey, strings.NewReader("thumb"), 5, "image/png"))
	require.NoError(t, conn.Create(&MatrixThumbnail{MediaID: m.MediaID, FilePath: "shared-thumbnail.png"}).Error)
	require.NoError(t, conn.Create(&MatrixMedia{MediaID: "other-owner", ServerName: m.ServerName, FilePath: sharedKey, ContentType: "image/png"}).Error)
	require.Equal(t, 1, gcRun(t, conn, now, true, "", 32).Retained)
	_, err := store.Stat(context.Background(), sharedKey)
	require.NoError(t, err)
}

func TestMediaGCOriginGraceCanOverridePreviousSchedule(t *testing.T) {
	conn, store, now := mediaGCFixture(t)
	seedGCMedia(t, conn, store, now, "remote-policy", "remote")
	result, err := CollectMedia(context.Background(), conn, now, MediaGCOptions{Apply: true})
	require.NoError(t, err)
	require.Equal(t, 1, result.Waiting)
	// A per-origin rule can shorten the default seven-day check schedule without
	// altering local media rules or waiting for the old schedule to elapse.
	require.Equal(t, 1, gcRun(t, conn, now.Add(24*time.Hour), true, "remote", 32).Deleted)
	seedGCMedia(t, conn, store, now, "longer-policy", "remote")
	gcRun(t, conn, now, true, "remote", 32)
	result, err = CollectMedia(context.Background(), conn, now.Add(24*time.Hour), MediaGCOptions{Apply: true})
	require.NoError(t, err)
	require.Equal(t, 1, result.Waiting)
	result, err = CollectMedia(context.Background(), conn, now.Add(25*time.Hour), MediaGCOptions{Apply: true})
	require.NoError(t, err)
	require.Zero(t, result.Scanned, "a longer policy must reschedule waiting records")
}

func TestMediaGCNewCandidatesNotStarvedByDueReferencedRows(t *testing.T) {
	conn, store, now := mediaGCFixture(t)
	active := seedGCMedia(t, conn, store, now, "active-backlog", "local")
	gcEvent(t, conn, "$active-backlog", active)
	require.NoError(t, conn.Model(&active).Update("gc_next_check_at", now.Add(-time.Hour)).Error)
	seedGCMedia(t, conn, store, now, "unseen", "remote")
	require.Equal(t, 1, gcRun(t, conn, now, true, "", 1).Waiting, "never-checked older records must not sort after every scheduled record")
}
