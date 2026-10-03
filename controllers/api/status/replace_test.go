package status

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
	"sync/atomic"
)

func replacementDB(t *testing.T) (*gorm.DB, *testutil.FixtureBuilder, *models.Account) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	all := append(append([]any{}, allTestModels...), &models.FederationOutbox{})
	for _, model := range all {
		stmt := &gorm.Statement{DB: conn}
		require.NoError(t, stmt.Parse(model))
		for _, field := range stmt.Schema.Fields {
			if field.AutoIncrement {
				field.DataType = schema.Int
			}
		}
	}
	require.NoError(t, conn.AutoMigrate(all...))
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	fix := testutil.NewFixtureBuilder(t, conn)
	user := fix.CreateUser()
	owner := fix.CreateAccount(user)
	return conn, fix, owner
}
func callReplacement(t *testing.T, owner *models.Account, id uint64, body any) *httptest.ResponseRecorder {
	t.Helper()
	data, err := json.Marshal(body)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/statuses/"+strconv.FormatUint(id, 10)+"/replace", bytes.NewReader(data))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: strconv.FormatUint(id, 10)}}
	c.Set("account", owner)
	safeHandler(c, Replace)
	return w
}
func decodedReplacementID(t *testing.T, w *httptest.ResponseRecorder) uint64 {
	t.Helper()
	require.Equal(t, 200, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	id, err := strconv.ParseUint(body["id"].(string), 10, 64)
	require.NoError(t, err)
	return id
}
func TestPutEditingIsExplicitlyUnsupported(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	Update(c)
	require.Equal(t, 405, w.Code)
	require.Equal(t, "GET, DELETE", w.Header().Get("Allow"))
	require.Contains(t, w.Body.String(), "delete and repost")
}
func TestReplacementPreservesMediaReplyVisibilityAndQueuesDeleteThenCreate(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	parent := fix.CreateStatus(owner, func(s *models.Status) { s.Content.Content = "parent" })
	original := fix.CreateStatus(owner, func(s *models.Status) {
		s.Content.Content = "original"
		s.Content.SpoilerText = "warning"
		s.Visibility = models.StatusVisibilityPrivate
		s.InReplyToId = &parent.Id
		s.InReplyToAccountId = &owner.Id
		s.Language = "zh"
		s.Sensitive = true
	})
	media := fix.CreateAttachment(func(a *models.Attachment) { a.AccountId = owner.Id; a.StatusId = &original.Id })
	var baseline int64
	require.NoError(t, conn.Model(&models.FederationOutbox{}).Count(&baseline).Error)
	nextID := decodedReplacementID(t, callReplacement(t, owner, original.Id, map[string]any{"status": "reposted"}))
	require.NotEqual(t, original.Id, nextID)
	require.ErrorIs(t, conn.First(&models.Status{}, original.Id).Error, gorm.ErrRecordNotFound)
	var next models.Status
	require.NoError(t, models.StatusLoadAllQuery(conn).First(&next, nextID).Error)
	require.Equal(t, "reposted", next.Content.Content)
	require.Equal(t, "warning", next.Content.SpoilerText)
	require.Equal(t, uint8(models.StatusVisibilityPrivate), next.Visibility)
	require.Equal(t, "zh", next.Language)
	require.True(t, next.Sensitive)
	require.Equal(t, parent.Id, *next.InReplyToId)
	require.Equal(t, owner.Id, *next.InReplyToAccountId)
	require.Len(t, next.Attachments, 1)
	require.Equal(t, media.Id, next.Attachments[0].Id)
	require.Equal(t, nextID, *next.Attachments[0].StatusId)
	var events []models.FederationOutbox
	require.NoError(t, conn.Order("id").Offset(int(baseline)).Find(&events).Error)
	require.Len(t, events, 2)
	require.Equal(t, "Delete", events[0].Kind)
	require.Equal(t, original.Id, events[0].ObjectID)
	require.Equal(t, "Create", events[1].Kind)
	require.Equal(t, nextID, events[1].ObjectID)
	var refreshedParent models.Status
	require.NoError(t, conn.First(&refreshedParent, parent.Id).Error)
	require.EqualValues(t, 1, refreshedParent.RepliesCount)
	require.Equal(t, 404, callReplacement(t, owner, original.Id, map[string]any{"status": "duplicate"}).Code)
}
func TestReplacementFailureRollsBackOriginalMediaAndOutbox(t *testing.T) {
	for _, stage := range []string{"delete", "outbox"} {
		t.Run(stage, func(t *testing.T) {
			conn, fix, owner := replacementDB(t)
			original := fix.CreateStatus(owner, func(s *models.Status) { s.Content.Content = "keep original" })
			media := fix.CreateAttachment(func(a *models.Attachment) { a.AccountId = owner.Id; a.StatusId = &original.Id })
			var baseline int64
			require.NoError(t, conn.Model(&models.FederationOutbox{}).Count(&baseline).Error)
			if stage == "delete" {
				require.NoError(t, conn.Callback().Delete().Before("gorm:delete").Register("test_fail_delete", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "statuses" {
						tx.AddError(errors.New("injected deletion failure"))
					}
				}))
			} else {
				require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("test_fail_outbox", func(tx *gorm.DB) {
					if event, ok := tx.Statement.Dest.(*models.FederationOutbox); ok && event.Kind == "Create" {
						tx.AddError(errors.New("injected durable event failure"))
					}
				}))
			}
			var triggered atomic.Int64
			listener := event.Register("status", "*", &event.Handler{Exec: func(e event.Event) bool { triggered.Add(1); return true }})
			t.Cleanup(func() { event.Remove("status", "*", listener) })
			w := callReplacement(t, owner, original.Id, map[string]any{"status": "must rollback"})
			require.Zero(t, triggered.Load(), "rolled-back replacement must not publish feed/notification events")
			require.Equal(t, 500, w.Code, w.Body.String())
			var saved models.Status
			require.NoError(t, conn.Preload("Content").First(&saved, original.Id).Error)
			require.Equal(t, "keep original", saved.Content.Content)
			require.NoError(t, conn.First(media, media.Id).Error)
			require.Equal(t, original.Id, *media.StatusId)
			var count int64
			require.NoError(t, conn.Model(&models.Status{}).Count(&count).Error)
			require.EqualValues(t, 1, count)
			require.NoError(t, conn.Model(&models.FederationOutbox{}).Count(&count).Error)
			require.Equal(t, baseline, count)
		})
	}
}
func TestReplacementClonesPollConfigurationWithoutVotes(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	expiry := time.Now().Add(time.Hour).UTC()
	original := fix.CreateStatus(owner, func(s *models.Status) {
		s.Content.Content = "question"
		s.Poll = &models.Poll{AccountId: owner.Id, ExpiresAt: &expiry, Multiple: true, Options: models.PollOptions{&models.PollOption{Content: "A", Index: 0, Votes: 3}, &models.PollOption{Content: "B", Index: 1, Votes: 2}}, Properties: map[string]any{"hide_totals": true}}
	})
	nextID := decodedReplacementID(t, callReplacement(t, owner, original.Id, map[string]any{"status": "reposted question"}))
	var next models.Status
	require.NoError(t, conn.Preload("Poll.Options").First(&next, nextID).Error)
	require.NotNil(t, next.Poll)
	require.NotEqual(t, original.Poll.Id, next.Poll.Id)
	require.True(t, next.Poll.Multiple)
	require.WithinDuration(t, expiry, *next.Poll.ExpiresAt, time.Millisecond)
	require.Equal(t, true, next.Poll.Properties["hide_totals"])
	require.Len(t, next.Poll.Options, 2)
	for _, option := range next.Poll.Options {
		require.Zero(t, option.Votes)
	}
}
func TestReplacementRejectsUnauthorizedAndInvalidMediaWithoutDeleting(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	original := fix.CreateStatus(owner, func(s *models.Status) { s.Content.Content = "keep" })
	otherUser := fix.CreateUser(func(u *models.User) { u.UserName = "replacement_outsider" })
	other := fix.CreateAccount(otherUser)
	require.Equal(t, 403, callReplacement(t, other, original.Id, map[string]any{"status": "hijack"}).Code)
	media := fix.CreateAttachment(func(a *models.Attachment) { a.AccountId = other.Id })
	require.Equal(t, 422, callReplacement(t, owner, original.Id, map[string]any{"media_ids": []string{strconv.FormatUint(media.Id, 10)}}).Code)
	require.NoError(t, conn.First(&models.Status{}, original.Id).Error)
}

func TestReplacementImportedLocalOnlyDropsHTMLAndPreservesFederationBoundary(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	original := fix.CreateStatus(owner, func(s *models.Status) {
		s.LocalOnly = true
		s.Content.Content = "original"
		s.Properties = map[string]any{"import_original_html": "<p><strong>old HTML</strong></p>"}
	})
	nextID := decodedReplacementID(t, callReplacement(t, owner, original.Id, map[string]any{"status": "new plain text"}))
	var next models.Status
	require.NoError(t, models.StatusLoadAllQuery(conn).First(&next, nextID).Error)
	require.True(t, next.LocalOnly)
	require.Nil(t, next.Properties["import_original_html"])
	require.Equal(t, "new plain text", next.Content.Content)
	var count int64
	require.NoError(t, conn.Model(&models.FederationOutbox{}).Where("object_id IN ?", []uint64{original.Id, nextID}).Count(&count).Error)
	require.Zero(t, count)
}
