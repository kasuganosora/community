package models

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/kasuganosora/sqlexec/pkg/api"
	sqlexecgorm "github.com/kasuganosora/sqlexec/pkg/api/gorm"
	"github.com/kasuganosora/sqlexec/pkg/resource/domain"
	"github.com/kasuganosora/sqlexec/pkg/resource/memory"
	"gorm.io/gorm"
)

// dbCache caches a single shared in-memory database for the models test package.
// We reuse one *gorm.DB across tests and migrate all models once, then truncate
// between tests to keep things fast and isolated enough for unit-style checks.
type dbCache struct {
	gormDB   *gorm.DB
	session  *api.Session
	apiDB    *api.DB
	once     sync.Once
	err      error
	migrated bool
}

var modelsDBCache dbCache

// setupTestDB returns a gorm.DB backed by an in-memory sqlexec database.
// It auto-migrates all models on first use.
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	modelsDBCache.once.Do(func() {
		if pgtest.Enabled() {
			modelsDBCache.gormDB, modelsDBCache.err = pgtest.OpenShared("t_models_pkg", &gorm.Config{SkipDefaultTransaction: true, DisableForeignKeyConstraintWhenMigrating: true})

			return
		}

		dsName := "models_test_" + uuid.New().String()[:8]

		db, err := api.NewDB(nil)
		if err != nil {
			modelsDBCache.err = err
			return
		}

		memDS := memory.NewMVCCDataSource(&domain.DataSourceConfig{
			Type:     domain.DataSourceTypeMemory,
			Name:     dsName,
			Writable: true,
		})
		if err := memDS.Connect(context.Background()); err != nil {
			_ = db.Close()
			modelsDBCache.err = err
			return
		}

		if err := db.RegisterDataSource(dsName, memDS); err != nil {
			_ = db.Close()
			modelsDBCache.err = fmt.Errorf("failed to register datasource: %w", err)
			return
		}

		if err := db.SetDefaultDataSource(dsName); err != nil {
			_ = db.Close()
			modelsDBCache.err = fmt.Errorf("failed to set default datasource: %w", err)
			return
		}

		session := db.Session()

		gormDB, err := gorm.Open(
			sqlexecgorm.NewDialector(session),
			&gorm.Config{SkipDefaultTransaction: true},
		)
		if err != nil {
			_ = session.Close()
			_ = db.Close()
			modelsDBCache.err = err
			return
		}

		modelsDBCache.apiDB = db
		modelsDBCache.session = session
		modelsDBCache.gormDB = gormDB
	})

	if modelsDBCache.err != nil {
		t.Fatalf("failed to set up test DB: %v", modelsDBCache.err)
	}

	if !modelsDBCache.migrated {
		if err := modelsDBCache.gormDB.AutoMigrate(allModels()...); err != nil {
			t.Fatalf("failed to auto-migrate: %v", err)
		}
		modelsDBCache.migrated = true
	}

	return modelsDBCache.gormDB
}

// allModels returns all model structs for auto-migration.
func allModels() []any {
	return []any{
		&User{}, &UserSecretData{}, &UserMeta{}, &UserSetting{},
		&Account{}, &AccountExtra{}, &AccountEmoji{},
		&Status{}, &StatusContent{}, &StatusMediaAttachment{}, &StatusTag{},
		&StatusEmoji{}, &StatusToAccount{}, &StatusHistory{},
		&FavoriteStatus{}, &BookmarkStatus{}, &PinStatus{},
		&Conversation{}, &AccountConversation{},
		&Follow{}, &Block{}, &MuteAccount{}, &MuteStatus{},
		&Notification{}, &Report{}, &Rule{}, &Tag{},
		&Emoji{}, &Filter{}, &FilterKeyword{},
		&Application{}, &Role{}, &RoleUser{},
		&Poll{}, &PollOption{}, &PollOptionVote{},
		&Announcement{}, &List{}, &ListAccount{}, &ListFollowAccount{},
		&Marker{}, &InviteCode{}, &DomainBlock{},
		&MediaAttachment{}, &Attachment{},
		&ProxyFollow{}, &AccountKey{}, &Relay{},
		&Group{}, &GroupMember{}, &GroupMessage{},
		&Contact{}, &ContactRequest{},
		&MatrixUser{}, &MatrixRoom{}, &MatrixMessage{},
		&SystemSetting{},
		&Thumbnail{}, &ThumbnailJob{}, &ThumbnailStats{}, &ThumbnailConfig{},
	}
}

// truncateAll clears all tables between tests for isolation.
func truncateAll(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, m := range allModels() {
		if err := db.Where("1 = 1").Delete(m).Error; err != nil {
			// Some models may not have a table; ignore errors.
			_ = err
		}
	}
}
