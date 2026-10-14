package commands

import (
	"bytes"
	"errors"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

func setupTagsTest(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.Tag{}, &models.StatusTag{}))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	return conn
}

func runTags(args ...string) cliResult {
	var out, errOut bytes.Buffer
	app := &cli.App{
		Name: "tootctl", Commands: []*cli.Command{TagsCommand()},
		Writer: &out, ErrWriter: &errOut,
		ExitErrHandler: func(*cli.Context, error) {},
	}
	err := app.Run(append([]string{"tootctl", "tags", "clean-invalid"}, args...))
	return cliResult{out: out.String(), errOut: errOut.String(), err: err}
}

func TestTagsCleanInvalidDryRunAndApply(t *testing.T) {
	conn := setupTagsTest(t)
	tags := []models.Tag{
		{Id: 1, Name: "<span>horror</span>", NameKey: "<span>horror</span>"},
		{Id: 2, Name: "<b>horror</b>", NameKey: "<b>horror</b>"},
		{Id: 3, Name: "39;ll", NameKey: "39;ll"},
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&tags).Error)
	require.NoError(t, conn.Create(&[]models.StatusTag{{StatusId: 1, TagId: 1}, {StatusId: 2, TagId: 2}, {StatusId: 3, TagId: 3}}).Error)
	var before []models.Tag
	require.NoError(t, conn.Order("id").Find(&before).Error)

	res := runTags()
	require.NoError(t, res.err)
	assert.Contains(t, res.out, "[dry run] invalid tags: 3 (rename 1, merge 1, delete 1), status links affected: 3")
	assert.Contains(t, res.out, "re-run with --apply")
	var original []models.Tag
	require.NoError(t, conn.Order("id").Find(&original).Error)
	assert.Equal(t, before, original, "default mode must leave every tag unchanged")
	var count int64
	require.NoError(t, conn.Model(&models.StatusTag{}).Count(&count).Error)
	assert.EqualValues(t, 3, count)

	res = runTags("--apply")
	require.NoError(t, res.err)
	assert.Contains(t, res.out, "invalid tags: 3 (rename 1, merge 1, delete 1)")
	assert.Contains(t, res.out, "done")
	var after []models.Tag
	require.NoError(t, conn.Find(&after).Error)
	require.Len(t, after, 1)
	assert.Equal(t, uint64(1), after[0].Id)
	assert.Equal(t, "horror", after[0].Name)
	assert.Equal(t, uint64(2), after[0].StatusCount)
	require.NoError(t, conn.Model(&models.StatusTag{}).Where("tag_id <> ?", 1).Count(&count).Error)
	assert.Zero(t, count)

	res = runTags("--apply")
	require.NoError(t, res.err)
	assert.Contains(t, res.out, "invalid tags: 0")
}

func TestTagsCleanInvalidApplyRollsBack(t *testing.T) {
	conn := setupTagsTest(t)
	tags := []models.Tag{{Id: 1, Name: "39;ll", NameKey: "39;ll"}, {Id: 2, Name: "#horror", NameKey: "horror"}}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&tags).Error)
	require.NoError(t, conn.Create(&models.StatusTag{StatusId: 1, TagId: 1}).Error)
	var before []models.Tag
	require.NoError(t, conn.Order("id").Find(&before).Error)
	wantErr := errors.New("simulated tag write failure")
	require.NoError(t, conn.Callback().Update().Before("gorm:update").Register("test:reject_tag_update", func(tx *gorm.DB) {
		if tx.Statement.Table == "tags" {
			tx.AddError(wantErr)
		}
	}))

	res := runTags("--apply")
	require.ErrorIs(t, res.err, wantErr)
	assert.NotContains(t, res.out, "done")
	var after []models.Tag
	require.NoError(t, conn.Order("id").Find(&after).Error)
	assert.Equal(t, before, after, "earlier deletes are rolled back with the failed rename")
	var links int64
	require.NoError(t, conn.Model(&models.StatusTag{}).Where("tag_id = ?", 1).Count(&links).Error)
	assert.EqualValues(t, 1, links)
}

func TestTagsCleanInvalidRejectsUnrecognizedScope(t *testing.T) {
	res := runTags("--apply", "horror")
	require.Error(t, res.err)
	assert.Contains(t, res.err.Error(), "takes no positional arguments")
	assert.Empty(t, res.out, "a mistaken scope must be rejected before accessing any database")
}
