package activitypub

import (
	"context"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func persistenceDB(t *testing.T, mysqlDB bool) *gorm.DB {
	t.Helper()
	var dialector gorm.Dialector = sqlite.Open(":memory:")
	if mysqlDB {
		dsn := os.Getenv("AP_MYSQL_TEST_DSN")
		if dsn == "" {
			t.Skip("AP_MYSQL_TEST_DSN not configured")
		}
		dialector = mysql.Open(dsn)
	}
	conn, err := gorm.Open(dialector, &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	if !mysqlDB {
		sqlDB.SetMaxOpenConns(1)
		require.NoError(t, conn.Exec("PRAGMA foreign_keys = ON").Error)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	all := []any{&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.RemoteActorIdentity{}, &ProcessedActivity{}, &models.Follow{}, &models.Status{}, &models.StatusContent{}, &models.Tag{}, &models.StatusTag{}, &models.StatusEmoji{}, &models.StatusToAccount{}, &models.Conversation{}, &models.AccountConversation{}, &models.Notification{}}
	if mysqlDB {
		all = all[:6]
	}
	if !mysqlDB {
		for _, m := range all {
			stmt := &gorm.Statement{DB: conn}
			require.NoError(t, stmt.Parse(m))
			for _, field := range stmt.Schema.Fields {
				if field.AutoIncrement {
					field.DataType = schema.Int
				}
			}
		}
	}
	require.NoError(t, conn.AutoMigrate(all...))
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	return conn
}

func testRemoteIdentityPersistence(t *testing.T, mysqlDB bool) {
	conn := persistenceDB(t, mysqlDB)
	actor := &Actor{ID: "https://remote.example/users/imported-" + strconv.FormatInt(time.Now().UnixNano(), 10), Type: "Person", PreferredUsername: "imported"}
	const concurrency = 8
	ids := make(chan uint64, concurrency)
	errs := make(chan error, concurrency)
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); a, err := createRemoteAccount(conn, actor); ids <- a.Id; errs <- err }()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var expected uint64
	for id := range ids {
		require.NotZero(t, id)
		if expected == 0 {
			expected = id
		}
		require.Equal(t, expected, id)
	}
	var count int64
	require.NoError(t, conn.Model(&models.Account{}).Where("origin_id = ?", actor.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.Model(&models.AccountExtra{}).Where("account_id = ?", expected).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ? AND user_id IS NULL", expected).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var loaded models.Account
	require.NoError(t, conn.Preload("User").First(&loaded, expected).Error)
	require.Zero(t, loaded.UserId)
	require.Nil(t, loaded.User)
	domain := "other.example"
	err := conn.Create(&models.Account{Username: "invalid_fk", Domain: &domain, UserId: 987654321}).Error
	require.Error(t, err, "the real users foreign key must still reject a missing nonzero owner")
}
func TestRemoteIdentitySQLite(t *testing.T) { testRemoteIdentityPersistence(t, false) }
func TestRemoteIdentityMySQL(t *testing.T)  { testRemoteIdentityPersistence(t, true) }

func testInboxFailureRollsBackAndSameIDRetries(t *testing.T, mysqlDB bool) {
	conn := persistenceDB(t, mysqlDB)
	domain := "remote.example"
	a := models.Account{Username: "retry", Domain: &domain}
	require.NoError(t, conn.Create(&a).Error)
	attempts := 0
	activityID := "https://remote.example/activity/retry-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	invoke := func(status int) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		processInboxTransaction(context.Background(), c, activityID, func(ctx context.Context) {
			attempts++
			require.NoError(t, activityDB(ctx).Model(&a).Update("display_name", "committed").Error)
			c.JSON(status, gin.H{"ok": status == 200})
		})
		return w.Code
	}
	require.Equal(t, 500, invoke(500))
	var count int64
	require.NoError(t, conn.Model(&ProcessedActivity{}).Where("activity_id = ?", activityID).Count(&count).Error)
	require.Zero(t, count)
	var loaded models.Account
	require.NoError(t, conn.First(&loaded, a.Id).Error)
	require.Empty(t, loaded.DisplayName)
	require.Equal(t, 200, invoke(200))
	require.Equal(t, 200, invoke(200))
	require.Equal(t, 2, attempts)
	require.NoError(t, conn.Model(&ProcessedActivity{}).Where("activity_id = ?", activityID).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestInboxFailureRollsBackAndSameIDRetries(t *testing.T) {
	testInboxFailureRollsBackAndSameIDRetries(t, false)
}
func TestInboxFailureMySQL(t *testing.T) { testInboxFailureRollsBackAndSameIDRetries(t, true) }

func TestDirectRecipientsIgnoreTextAndTagOnlyMentions(t *testing.T) {
	conn := persistenceDB(t, false)
	recipient := models.Account{Username: "recipient"}
	outsider := models.Account{Username: "outsider"}
	require.NoError(t, conn.Create(&recipient).Error)
	require.NoError(t, conn.Create(&outsider).Error)
	// This helper is exercised with account actors resolved from explicit to/cc.
	obj := map[string]any{"to": []any{systemsettings.GetBaseURL() + "/users/recipient"}, "content": "@outsider", "tag": []any{map[string]any{"type": "Mention", "href": systemsettings.GetBaseURL() + "/users/outsider"}}}
	t.Setenv("APP_BASE_URL", "https://localhost")
	accounts, err := recipientAccounts(context.Background(), obj, false)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, recipient.Id, accounts[0].Id)
}

func TestInboundDirectPersistsTimestampedRecipientsBeforeConversation(t *testing.T) {
	conn := persistenceDB(t, false)
	domain := "remote.example"
	author := models.Account{Username: "author", Domain: &domain, OriginId: "https://remote.example/users/author"}
	recipient := models.Account{Username: "recipient"}
	outsider := models.Account{Username: "outsider"}
	for _, a := range []*models.Account{&author, &recipient, &outsider} {
		require.NoError(t, conn.Create(a).Error)
	}
	base := systemsettings.GetBaseURL()
	object := map[string]any{"id": "https://remote.example/notes/direct", "type": "Note", "attributedTo": author.OriginId, "content": "A private message mentioning @outsider", "to": []any{base + "/users/recipient"}, "tag": []any{map[string]any{"type": "Mention", "href": base + "/users/outsider"}}}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx := context.WithValue(context.Background(), actorKey, author.OriginId)
	processInboxTransaction(ctx, c, "https://remote.example/activity/direct", func(ctx context.Context) { handleCreateActivity(ctx, c, map[string]any{"object": object}, &recipient) })
	require.Equal(t, 200, w.Code, w.Body.String())
	var status models.Status
	require.NoError(t, conn.Preload("Content").Preload("ToAccounts").Where("origin_uri = ?", object["id"]).First(&status).Error)
	require.Equal(t, uint8(models.StatusVisibilityDirect), status.Visibility)
	require.Len(t, status.ToAccounts, 1)
	require.Equal(t, recipient.Id, status.ToAccounts[0].Id)
	var join models.StatusToAccount
	require.NoError(t, conn.Where("status_id = ?", status.Id).First(&join).Error)
	require.NotNil(t, join.CreatedAt)
	require.NotNil(t, status.ConversationId)
	var count int64
	require.NoError(t, conn.Model(&models.AccountConversation{}).Where("conversation_id = ? AND account_id = ?", *status.ConversationId, recipient.Id).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.Model(&models.AccountConversation{}).Where("conversation_id = ? AND account_id = ?", *status.ConversationId, outsider.Id).Count(&count).Error)
	require.Zero(t, count)
}

func TestFollowAcceptanceAuthenticatesReferenceAndCountsOnce(t *testing.T) {
	conn := persistenceDB(t, false)
	local := models.Account{Username: "local"}
	domain := "remote.example"
	remote := models.Account{Username: "remote", Domain: &domain, OriginId: "https://remote.example/users/remote"}
	require.NoError(t, conn.Create(&local).Error)
	require.NoError(t, conn.Create(&remote).Error)
	follow := models.Follow{AccountId: local.Id, FollowId: remote.Id, Status: models.FollowStatusRequested}
	require.NoError(t, conn.Create(&follow).Error)
	uri := systemsettings.GetBaseURL() + "/users/local"
	activity := map[string]any{"object": map[string]any{"type": "Follow", "id": uri + "/follows/" + strconv.FormatUint(follow.Id, 10), "actor": uri, "object": remote.OriginId}}
	invoke := func(actor string) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		handleAcceptActivity(context.WithValue(context.Background(), actorKey, actor), c, activity, &local)
		return w.Code
	}
	require.Equal(t, 403, invoke("https://remote.example/users/attacker"))
	require.Equal(t, 200, invoke(remote.OriginId))
	require.Equal(t, 200, invoke(remote.OriginId))
	require.NoError(t, conn.First(&follow, follow.Id).Error)
	require.Equal(t, models.FollowStatusFollowed, follow.Status)
	var extra models.AccountExtra
	require.NoError(t, conn.First(&extra, "account_id = ?", local.Id).Error)
	require.Equal(t, 1, extra.FollowingCount)
	var remoteExtra models.AccountExtra
	require.NoError(t, conn.First(&remoteExtra, "account_id = ?", remote.Id).Error)
	require.Equal(t, 1, remoteExtra.FollowersCount)
}
