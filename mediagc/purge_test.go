package mediagc

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openDB(t *testing.T) *gorm.DB {
	t.Helper()

	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.MediaAttachment{}))

	return conn
}

func seedMedia(t *testing.T, db *gorm.DB, filePath string, statusID *uint64, updated time.Time) *models.MediaAttachment {
	t.Helper()

	thumb := "thumb_" + filepath.Base(filePath)
	row := &models.MediaAttachment{
		AccountId: 1, FileName: filepath.Base(filePath), FilePath: filePath, ThumbnailPath: &thumb,
		ContentType: "image/png", Type: models.MediaTypeImage, StatusId: statusID,
	}
	require.NoError(t, db.Create(row).Error)
	require.NoError(t, db.Model(row).Where("id = ?", row.Id).UpdateColumns(map[string]any{
		"updated_at": updated,
		"created_at": updated,
		"status_id":  statusID,
	}).Error)
	require.NoError(t, db.First(row, row.Id).Error)

	return row
}

func putObjects(t *testing.T, backend storage.Backend, row *models.MediaAttachment) {
	t.Helper()

	ctx := context.Background()
	require.NoError(t, backend.Put(ctx, row.StorageKey(), strings.NewReader("orig"), 4, "image/png"))
	require.NoError(t, backend.Put(ctx, row.ThumbnailStorageKey(), strings.NewReader("thumb"), 5, "image/png"))
}

func objectExists(t *testing.T, backend storage.Backend, key string) bool {
	t.Helper()

	_, err := backend.Stat(context.Background(), key)
	if errors.Is(err, storage.ErrNotFound) {
		return false
	}

	require.NoError(t, err)

	return true
}

func TestPurgeDeletesExpiredUnattachedObjectsOnly(t *testing.T) {
	db := openDB(t)
	backend := storagetest.NewMemory()
	t.Cleanup(storage.SetDefault(backend))
	old := time.Now().Add(-8 * 24 * time.Hour)
	recent := time.Now().Add(-time.Minute)
	expired := seedMedia(t, db, "2026/10/01/gone.png", nil, old)
	young := seedMedia(t, db, "2026/10/01/draft.png", nil, recent)
	liveID := uint64(40)
	live := seedMedia(t, db, "2026/10/01/posted.png", &liveID, old)
	putObjects(t, backend, expired)
	putObjects(t, backend, young)
	putObjects(t, backend, live)

	stats, err := Purge(context.Background(), db, Options{MinAge: DefaultMinAge, Limit: 20})
	require.NoError(t, err)
	require.Equal(t, 1, stats.Removed)
	require.Equal(t, 1, stats.Objects)
	require.Zero(t, stats.Failed)

	require.ErrorIs(t, db.First(&models.MediaAttachment{}, expired.Id).Error, gorm.ErrRecordNotFound)
	require.False(t, objectExists(t, backend, expired.StorageKey()))
	require.False(t, objectExists(t, backend, expired.ThumbnailStorageKey()))

	require.NoError(t, db.First(&models.MediaAttachment{}, young.Id).Error)
	require.True(t, objectExists(t, backend, young.StorageKey()))
	require.NoError(t, db.First(&models.MediaAttachment{}, live.Id).Error)
	require.True(t, objectExists(t, backend, live.StorageKey()))
	require.True(t, objectExists(t, backend, live.ThumbnailStorageKey()))
}

func TestPurgeDryRunDoesNotDelete(t *testing.T) {
	db := openDB(t)
	backend := storagetest.NewMemory()
	t.Cleanup(storage.SetDefault(backend))
	row := seedMedia(t, db, "2026/10/01/soon.png", nil, time.Now().Add(-8*24*time.Hour))
	putObjects(t, backend, row)

	stats, err := Purge(context.Background(), db, Options{MinAge: DefaultMinAge, DryRun: true, Limit: 20})
	require.NoError(t, err)
	require.Equal(t, 1, stats.WouldRemove)
	require.Zero(t, stats.Removed)
	require.NoError(t, db.First(&models.MediaAttachment{}, row.Id).Error)
	require.True(t, objectExists(t, backend, row.StorageKey()))
	require.True(t, objectExists(t, backend, row.ThumbnailStorageKey()))
}

func TestPurgeKeepsSharedAndProfileObjects(t *testing.T) {
	db := openDB(t)
	backend := storagetest.NewMemory()
	t.Cleanup(storage.SetDefault(backend))
	old := time.Now().Add(-8 * 24 * time.Hour)
	shared := seedMedia(t, db, "2026/10/01/shared.png", nil, old)
	liveID := uint64(7)
	kept := seedMedia(t, db, "2026/10/01/shared.png", &liveID, old)
	putObjects(t, backend, shared)

	profile := seedMedia(t, db, "2026/10/01/avatar.png", nil, old)
	putObjects(t, backend, profile)
	avatar := "/media/" + strconv.FormatUint(profile.Id, 10) + "/avatar.png"
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&models.Account{Id: 1, Username: "profile", Acct: "profile", DisplayName: "profile", Avatar: avatar}).Error)

	require.NoError(t, db.Exec(`CREATE TABLE matrix_media (id integer primary key, file_path text)`).Error)
	matrix := seedMedia(t, db, "2026/10/01/bridged.png", nil, old)
	putObjects(t, backend, matrix)
	require.NoError(t, db.Exec(`INSERT INTO matrix_media (id, file_path) VALUES (1, ?)`, matrix.StorageKey()).Error)

	stats, err := Purge(context.Background(), db, Options{MinAge: DefaultMinAge, Limit: 20})
	require.NoError(t, err)
	require.Equal(t, 2, stats.Removed)
	require.Equal(t, 2, stats.SharedKept)
	require.Equal(t, 1, stats.ProfileKept)
	require.Zero(t, stats.Objects)

	require.ErrorIs(t, db.First(&models.MediaAttachment{}, shared.Id).Error, gorm.ErrRecordNotFound)
	require.NoError(t, db.First(&models.MediaAttachment{}, kept.Id).Error)
	require.True(t, objectExists(t, backend, shared.StorageKey()))
	require.True(t, objectExists(t, backend, shared.ThumbnailStorageKey()))

	require.NoError(t, db.First(&models.MediaAttachment{}, profile.Id).Error)
	require.True(t, objectExists(t, backend, profile.StorageKey()))

	require.ErrorIs(t, db.First(&models.MediaAttachment{}, matrix.Id).Error, gorm.ErrRecordNotFound)
	require.True(t, objectExists(t, backend, matrix.StorageKey()))
}

type failDelete struct{ storage.Backend }

func (failDelete) Delete(context.Context, string) error {
	return errors.New("object storage unavailable")
}

func TestPurgeStorageFailureKeepsTheRow(t *testing.T) {
	db := openDB(t)
	backend := storagetest.NewMemory()
	row := seedMedia(t, db, "2026/10/01/retry.png", nil, time.Now().Add(-8*24*time.Hour))
	putObjects(t, backend, row)
	t.Cleanup(storage.SetDefault(failDelete{backend}))

	stats, err := Purge(context.Background(), db, Options{MinAge: DefaultMinAge, Limit: 20})
	require.Error(t, err)
	require.Equal(t, 1, stats.Failed)
	require.Zero(t, stats.Removed)
	require.NoError(t, db.First(&models.MediaAttachment{}, row.Id).Error)
	require.True(t, objectExists(t, backend, row.StorageKey()))
	require.True(t, objectExists(t, backend, row.ThumbnailStorageKey()))
}

func TestParseMinAge(t *testing.T) {
	age, err := ParseMinAge("")
	require.NoError(t, err)
	require.Equal(t, DefaultMinAge, age)
	require.Equal(t, 7*24*time.Hour, age)

	_, err = ParseMinAge("10m")
	require.Error(t, err)
	_, err = ParseMinAge("nope")
	require.Error(t, err)
	age, err = ParseMinAge("48h")
	require.NoError(t, err)
	require.Equal(t, 48*time.Hour, age)
	require.True(t, Enabled(""))
	require.False(t, Enabled("off"))
}
