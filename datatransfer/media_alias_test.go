package datatransfer

import (
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func aliasDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, err)
	sql, err := db.DB()
	require.NoError(t, err)
	sql.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sql.Close() })
	require.NoError(t, db.AutoMigrate(&ImportedMediaAlias{}, &models.MediaAttachment{}))
	for _, id := range []uint64{1, 2} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&models.MediaAttachment{Id: id, FileName: "random.png", FilePath: "imports/random.png"}).Error)
	}
	return db
}

func TestMediaAliasIdempotencyConflictAndRollback(t *testing.T) {
	db := aliasDB(t)
	o := Options{Scope: "site", TargetBaseURL: "https://social.test"}
	m := Media{OriginalURL: "https://social.test/system/media_attachments/files/1/original/a.png"}
	require.NoError(t, RecordMediaAlias(db, o, m, 1))
	require.NoError(t, RecordMediaAlias(db, o, m, 1))
	require.Error(t, RecordMediaAlias(db, o, m, 2))
	target, err := ResolveMediaAlias(db, o.TargetBaseURL, "/system/media_attachments/files/1/original/a.png")
	require.NoError(t, err)
	require.Equal(t, "/media/1/random.png", target)
	err = db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, RecordMediaAlias(tx, o, Media{OriginalURL: "https://social.test/files/rolled-back"}, 1))
		return errors.New("rollback")
	})
	require.Error(t, err)
	_, err = ResolveMediaAlias(db, o.TargetBaseURL, "/files/rolled-back")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	require.NoError(t, RecordMediaAlias(db, Options{Scope: "personal", TargetBaseURL: o.TargetBaseURL}, Media{OriginalURL: "https://social.test/files/personal"}, 1))
	_, err = ResolveMediaAlias(db, o.TargetBaseURL, "/files/personal")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestMediaAliasRejectsPrivilegedPathsAndExternalOrigins(t *testing.T) {
	db := aliasDB(t)
	o := Options{Scope: "site", TargetBaseURL: "https://social.test"}
	for _, raw := range []string{"https://other.test/files/a", "https://name:password@social.test/files/a", "https://social.test/files/a?secret=1", "https://social.test/files/a#fragment", "https://social.test/files/a?", "https://social.test/files/a#", "https://social.test/api/token", "https://social.test/_matrix/media/a", "https://social.test/oauth/login", "https://social.test/files/../api/token", "https://social.test/files/%2e%2e/api/token", "https://social.test/files/a%2fb", "https://social.test/files/a%5cb", "https://social.test/files/a%0ab", "https://social.test/files/"} {
		t.Run(raw, func(t *testing.T) { require.Error(t, RecordMediaAlias(db, o, Media{OriginalURL: raw}, 1)) })
	}
	require.NoError(t, db.Model(&models.MediaAttachment{}).Where("id = ?", 2).Update("file_path", "").Error)
	require.Error(t, RecordMediaAlias(db, o, Media{OriginalURL: "https://social.test/files/remote"}, 2))
}
