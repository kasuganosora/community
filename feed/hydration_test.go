package feed

import (
	"encoding/json"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
	"time"
)

func TestFlatTimelineHydrationMatchesNestedPreloads(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()
	require.NoError(t, conn.AutoMigrate(&models.Status{}, &models.Account{}, &models.AccountExtra{}, &models.StatusContent{}, &models.Application{}, &models.Attachment{}, &models.Emoji{}, &models.Poll{}, &models.PollOption{}, &models.Tag{}))
	write := conn.Session(&gorm.Session{SkipHooks: true})
	for _, id := range []uint64{1, 2, 3} {
		require.NoError(t, write.Create(&models.Account{Id: id}).Error)
		require.NoError(t, write.Create(&models.AccountExtra{AccountId: id}).Error)
	}
	original, reply, app := uint64(10), uint64(3), uint64(1)
	applicationUUID := uuid.New()
	require.NoError(t, write.Create(&models.Application{Id: app, Uuid: &applicationUUID}).Error)
	for _, status := range []*models.Status{{Id: 10, AccountId: 1, InReplyToAccountId: &reply, ApplicationId: &app}, {Id: 20, AccountId: 2, ReblogId: &original, InReplyToAccountId: &reply, ApplicationId: &app}} {
		require.NoError(t, write.Create(status).Error)
		require.NoError(t, write.Create(&models.Attachment{Id: status.Id, AccountId: status.AccountId, StatusId: &status.Id, FileName: "photo.png", Description: "alt", ContentType: "image/png"}).Error)
		require.NoError(t, write.Create(&models.StatusContent{StatusId: status.Id, Content: "content"}).Error)
		expires := time.Now().Add(time.Hour)
		require.NoError(t, write.Create(&models.Poll{Id: status.Id, StatusId: status.Id, AccountId: status.AccountId, ExpiresAt: &expires}).Error)
		require.NoError(t, write.Create(&models.PollOption{Id: status.Id, PollId: status.Id, Content: "option", Votes: 7}).Error)
		require.NoError(t, write.Exec("INSERT INTO status_to_accounts (status_id, account_id) VALUES (?, ?)", status.Id, 3).Error)
	}
	require.NoError(t, write.Create(&models.Tag{Id: 1, Name: "hydration"}).Error)
	require.NoError(t, write.Create(&models.Emoji{Id: 1, Shortcode: "hydration"}).Error)
	for _, id := range []uint64{10, 20} {
		require.NoError(t, write.Exec("INSERT INTO status_tags (status_id,tag_id) VALUES (?,1)", id).Error)
		require.NoError(t, write.Exec("INSERT INTO status_emojis (status_id,emoji_id) VALUES (?,1)", id).Error)
	}
	queries := 0
	require.NoError(t, conn.Callback().Query().After("gorm:query").Register("test:count", func(*gorm.DB) { queries++ }))
	old := models.Statuses{}
	require.NoError(t, models.StatusLoadAllQuery(conn).Where("id = ?", 20).Find(&old).Error)
	nestedQueries := queries
	queries = 0
	got, err := loadTimelineStatuses(conn, models.Statuses{{Id: 20, ReblogId: &original}})
	require.NoError(t, err)
	flatQueries := queries
	oldJSON, err := json.Marshal(old)
	require.NoError(t, err)
	gotJSON, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, string(oldJSON), string(gotJSON), "all root and original metadata must survive")
	require.Less(t, flatQueries, nestedQueries)
	t.Logf("nested hydration=%d SQL queries; flat hydration=%d", nestedQueries, flatQueries)
}
