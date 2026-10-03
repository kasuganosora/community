package datatransfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	gmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestImportMySQLConcurrentBundleKeepsOnePostAndMediaFile(t *testing.T) {
	dsn := os.Getenv("COMMUNITY_MIGRATION_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires isolated lab MySQL DSN")
	}
	cfg, e := mysql.ParseDSN(dsn)
	require.NoError(t, e)
	cfg.DBName = ""
	admin, e := gorm.Open(gmysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Discard})
	require.NoError(t, e)
	adminSQL, e := admin.DB()
	require.NoError(t, e)
	defer adminSQL.Close()
	name := fmt.Sprintf("qa_transfer_concurrent_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE DATABASE `"+name+"`").Error)
	defer admin.Exec("DROP DATABASE `" + name + "`")
	cfg.DBName = name
	db, e := gorm.Open(gmysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Discard, DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, e)
	raw, e := db.DB()
	require.NoError(t, e)
	raw.SetMaxOpenConns(4)
	raw.SetMaxIdleConns(4)
	defer raw.Close()
	require.NoError(t, db.AutoMigrate(&models.User{}, &models.UserSecretData{}, &models.UserSetting{}, &models.Account{}, &models.AccountExtra{}, &models.AccountKey{}, &models.AccountFederationKey{}, &models.Status{}, &models.StatusContent{}, &models.StatusToAccount{}, &models.StatusEmoji{}, &models.StatusTag{}, &models.Tag{}, &models.MediaAttachment{}, &models.Emoji{}, &models.Conversation{}, &models.AccountConversation{}, &models.Follow{}, &models.Block{}, &models.MuteAccount{}, &models.List{}, &models.ListAccount{}, &models.ListFollowAccount{}, &models.Poll{}, &models.PollOption{}, &models.PollOptionVote{}, &models.StatusReaction{}, &models.FavoriteStatus{}, &models.BookmarkStatus{}, &models.PinStatus{}, &models.FederationOutbox{}, &ImportReceipt{}, &StoredPreservedRecord{}, &ImportedMediaAlias{}))
	u := models.User{UUID: "mysql-concurrency", UserName: "mysql-owner", EmailHash: "mysql-owner-hash", Status: models.UserStatusNormal}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&u).Error)
	a := models.Account{UserId: u.Id, Username: "mysql-owner", Acct: "mysql-owner@__LOCAL__"}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&a).Error)
	require.NoError(t, db.Create(&models.AccountExtra{AccountId: a.Id}).Error)
	data := []byte("concurrent import immutable file contents")
	sum := sha256.Sum256(data)
	sourceDir := t.TempDir()
	path := filepath.Join(sourceDir, "original.bin")
	require.NoError(t, os.WriteFile(path, data, 0600))
	o := Options{Scope: "personal", OwnerAccountID: a.Id, OwnerUserID: u.Id, TargetBaseURL: "https://new.example", StorageDir: t.TempDir(), ArchiveDir: sourceDir, Files: map[string]ArchiveFile{"media.bin": {Name: "media.bin", Path: path, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}}}
	b := &Bundle{Schema: Schema, Scope: "personal", Source: Source{Platform: "community", BaseURL: "https://old.example"}, OwnerID: "a", Accounts: []Account{{ID: "a", Username: "original", Local: true, ActorURI: "https://old.example/users/original"}}, Media: []Media{{ID: "m", OwnerID: "a", Path: "media.bin", Name: "original.bin", ContentType: "application/octet-stream", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}}, Posts: []Post{{ID: "p", AuthorID: "a", Text: "exactly one", Visibility: "public", MediaIDs: []string{"m"}, CreatedAt: time.Now().UTC()}}}
	// The barrier runs after each worker has staged its own media bytes and
	// before either transaction begins: these are genuinely overlapping imports.
	barrier := make(chan struct{})
	var arrived sync.WaitGroup
	arrived.Add(2)
	type result struct {
		report *Report
		err    error
	}
	out := make(chan result, 2)
	for n := 0; n < 2; n++ {
		go func() {
			r, e := Import(context.Background(), db, b, o, func(p Progress) {
				if p.Phase == "importing" {
					arrived.Done()
					<-barrier
				}
			})
			out <- result{r, e}
		}()
	}
	ready := make(chan struct{})
	go func() { arrived.Wait(); close(ready) }()
	select {
	case <-ready:
		close(barrier)
	case <-time.After(10 * time.Second):
		close(barrier)
		t.Fatal("concurrent imports did not reach staging barrier")
	}
	created, skipped := 0, 0
	for n := 0; n < 2; n++ {
		r := <-out
		require.NoError(t, r.err)
		created += r.report.Created["post"]
		skipped += r.report.Skipped["post"]
	}
	require.Equal(t, 1, created)
	require.Equal(t, 1, skipped)
	for _, model := range []any{&models.Status{}, &models.MediaAttachment{}} {
		var count int64
		require.NoError(t, db.Model(model).Count(&count).Error)
		require.EqualValues(t, 1, count)
	}
	var receipts int64
	require.NoError(t, db.Model(&ImportReceipt{}).Where("kind = ?", "post").Count(&receipts).Error)
	require.EqualValues(t, 1, receipts)
	require.NoError(t, db.Model(&ImportReceipt{}).Where("kind = ?", "media").Count(&receipts).Error)
	require.EqualValues(t, 1, receipts)
	files, e := filepath.Glob(filepath.Join(o.StorageDir, "imports", "*"))
	require.NoError(t, e)
	require.Len(t, files, 1)
	saved, e := os.ReadFile(files[0])
	require.NoError(t, e)
	require.Equal(t, data, saved)
	var extra models.AccountExtra
	require.NoError(t, db.First(&extra, "account_id = ?", a.Id).Error)
	require.Equal(t, 1, extra.StatusesCount)
}
