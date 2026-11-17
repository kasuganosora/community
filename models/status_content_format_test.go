package models

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestStatusExplicitHTMLFormatAndEditableSource(t *testing.T) {
	s := Status{Content: StatusContent{Content: `<p>Hello <strong>world</strong></p><p><a href="https://example.org">link</a></p><script>bad()</script>`, ContentFormat: "html"}}
	require.Contains(t, s.ContentHTML(htmlsafe.RenderOptions{}), "<strong>world</strong>")
	require.NotContains(t, s.ContentHTML(htmlsafe.RenderOptions{}), "script")
	text := s.Content.EditableText()
	require.Contains(t, text, "Hello world\n")
	require.Contains(t, text, "https://example.org")
	require.NotContains(t, text, "<p>")
	s.Content.ContentFormat = "unknown"
	require.Contains(t, s.ContentHTML(htmlsafe.RenderOptions{}), "&lt;p&gt;")
}

func openStatusContentParentIDTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger:                                   logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	require.NoError(t, conn.AutoMigrate(allModels()...))
	tx := conn.Session(&gorm.Session{SkipHooks: true})
	now := time.Now()
	require.NoError(t, tx.Create(&User{Id: 1, UUID: "u-1", UserName: "alice", EmailHash: "h-1", Status: UserStatusNormal}).Error)
	require.NoError(t, tx.Omit("Extra", "User").Create(&Account{Id: 1, UserId: 1, Username: "alice", Acct: "alice", CreatedAt: &now, UpdatedAt: &now}).Error)
	return conn
}

func TestStatusContentRejectsZeroStatusID(t *testing.T) {
	conn := openStatusContentParentIDTestDB(t)

	require.Error(t, conn.Create(&StatusContent{Content: "orphan text"}).Error)
	require.Error(t, conn.Create(&StatusContent{}).Error)
	sc := StatusContent{Content: "via save with zero id"}
	require.Error(t, conn.Save(&sc).Error)

	var count int64
	require.NoError(t, conn.Model(&StatusContent{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestStatusContentUsesParentStatusIDDespiteLaggedSequence(t *testing.T) {
	conn := openStatusContentParentIDTestDB(t)
	tx := conn.Session(&gorm.Session{SkipHooks: true})
	now := time.Now()

	// Seed small occupied content IDs (1..3) and a high existing ID (10000).
	for _, id := range []uint64{1, 2, 3, 10000} {
		require.NoError(t, tx.Omit("Content").Create(&Status{Id: id, AccountId: 1, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now}).Error)
		require.NoError(t, tx.Create(&StatusContent{StatusId: id, ContentFormat: "plain", Content: "seed"}).Error)
	}
	// Simulate a lagged sequence on status_contents (as happens when rows were inserted with explicit IDs).
	_ = conn.Exec("UPDATE sqlite_sequence SET seq = 1 WHERE name = 'status_contents'").Error

	textPost := &Status{AccountId: 1, Visibility: StatusVisibilityPublic, Content: StatusContent{Content: "hello #world"}}
	require.NoError(t, conn.Create(textPost).Error)
	require.Greater(t, textPost.Id, uint64(10000))
	var gotText StatusContent
	require.NoError(t, conn.First(&gotText, textPost.Id).Error)
	require.Equal(t, textPost.Id, gotText.StatusId)
	require.Equal(t, "hello #world", gotText.Content)

	// Media-only / empty-text non-reblog status must also get a status_contents row with parent status.Id.
	emptyTextPost := &Status{AccountId: 1, Visibility: StatusVisibilityPublic, Content: StatusContent{}}
	require.NoError(t, conn.Create(emptyTextPost).Error)
	var gotEmpty StatusContent
	require.NoError(t, conn.First(&gotEmpty, emptyTextPost.Id).Error)
	require.Equal(t, emptyTextPost.Id, gotEmpty.StatusId)

	// Pure reblog legitimately has NO status_contents row.
	reblogTarget := textPost.Id
	reblog := &Status{AccountId: 1, Visibility: StatusVisibilityPublic, ReblogId: &reblogTarget}
	require.NoError(t, conn.Create(reblog).Error)
	var reblogContentCount int64
	require.NoError(t, conn.Model(&StatusContent{}).Where("status_id = ?", reblog.Id).Count(&reblogContentCount).Error)
	require.Zero(t, reblogContentCount, "pure reblogs legitimately have no status_contents row")
}

func TestStatusContentConflictIsIdempotent(t *testing.T) {
	conn := openStatusContentParentIDTestDB(t)
	now := time.Now()
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Content").Create(&Status{
		Id: 500, AccountId: 1, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now,
	}).Error)

	first := &StatusContent{StatusId: 500, Content: "first #tagone"}
	require.NoError(t, conn.Create(first).Error)

	duplicate := &StatusContent{StatusId: 500, Content: "second #tagone #tagtwo"}
	require.NoError(t, conn.Create(duplicate).Error)

	var stored StatusContent
	require.NoError(t, conn.First(&stored, 500).Error)
	require.Equal(t, "first #tagone", stored.Content)

	var total int64
	require.NoError(t, conn.Model(&StatusContent{}).Count(&total).Error)
	require.EqualValues(t, 1, total)

	var tag Tag
	require.NoError(t, conn.Where("name_key = ?", "tagone").First(&tag).Error)
	require.EqualValues(t, 1, tag.StatusCount)
}

func TestInspectAndBackfillMissingStatusContentsDistinguishesReblogs(t *testing.T) {
	conn := openStatusContentParentIDTestDB(t)
	tx := conn.Session(&gorm.Session{SkipHooks: true})
	now := time.Now()
	remoteDomain := "remote.example"
	remoteURI := "https://remote.example/notes/902"

	require.NoError(t, tx.Omit("Content").Create(&Status{Id: 900, AccountId: 1, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now}).Error)
	require.NoError(t, tx.Create(&StatusContent{StatusId: 900, Content: "present"}).Error)

	require.NoError(t, tx.Omit("Content").Create(&Status{Id: 901, AccountId: 1, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now}).Error)
	require.NoError(t, tx.Omit("Content").Create(&Status{Id: 902, AccountId: 1, Domain: &remoteDomain, OriginUri: &remoteURI, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now}).Error)

	parentID := uint64(900)
	require.NoError(t, tx.Omit("Content").Create(&Status{Id: 903, AccountId: 1, ReblogId: &parentID, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now}).Error)

	report, err := InspectStatusContents(context.Background(), conn)
	require.NoError(t, err)
	require.EqualValues(t, 2, report.MissingNonReblogTotal)
	require.EqualValues(t, 1, report.MissingLocal)
	require.EqualValues(t, 1, report.MissingRemote)
	require.EqualValues(t, 1, report.ReblogsWithoutContent)

	repairedDry, err := BackfillMissingStatusContents(context.Background(), conn, true)
	require.NoError(t, err)
	require.EqualValues(t, 2, repairedDry)
	var afterDry int64
	require.NoError(t, conn.Model(&StatusContent{}).Count(&afterDry).Error)
	require.EqualValues(t, 1, afterDry)

	repaired, err := BackfillMissingStatusContents(context.Background(), conn, false)
	require.NoError(t, err)
	require.EqualValues(t, 2, repaired)

	for _, id := range []uint64{901, 902} {
		var sc StatusContent
		require.NoError(t, conn.First(&sc, id).Error)
		require.Equal(t, id, sc.StatusId)
	}
	var reblogCount int64
	require.NoError(t, conn.Model(&StatusContent{}).Where("status_id = ?", 903).Count(&reblogCount).Error)
	require.Zero(t, reblogCount, "reblog must remain without status_contents row")

	repairedAgain, err := BackfillMissingStatusContents(context.Background(), conn, false)
	require.NoError(t, err)
	require.Zero(t, repairedAgain)
}
