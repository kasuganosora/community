package conversation

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestList_OnlyVisibleDirectMessagesBeforePagination(t *testing.T) {
	// Exercise real SQL subquery and NULL semantics: the in-memory sqlexec
	// test adapter does not implement the nested IN filtering used here.
	connDB, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := connDB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	testDB = &testutil.TestDB{GormDB: connDB}
	database.SetTestDB(connDB)
	require.NoError(t, testDB.AutoMigrate(&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.Status{}, &models.StatusContent{}, &models.PinStatus{}, &models.Conversation{}, &models.AccountConversation{}))
	require.NoError(t, testDB.AutoMigrate(&models.Poll{}, &models.PollOption{}, &models.PollOptionVote{}, &models.Application{}, &models.Tag{}, &models.Emoji{}, &models.MediaAttachment{}, &models.Follow{}, &models.StatusToAccount{}, &models.StatusEmoji{}, &models.AccountEmoji{}, &models.StatusTag{}, &models.StatusMediaAttachment{}, &models.FavoriteStatus{}, &models.BookmarkStatus{}, &models.Filter{}, &models.MuteAccount{}, &models.Block{}, &models.MuteStatus{}))
	newAccount := func(id uint64, name string) *models.Account {
		account := &models.Account{Id: id, Username: name}
		require.NoError(t, connDB.Create(account).Error)
		return account
	}
	alice := newAccount(1, "alice")
	bob := newAccount(2, "bob")
	carol := newAccount(3, "carol")
	conn := testDB.GormDB.Session(&gorm.Session{SkipHooks: true})
	addStatus := func(conv *models.Conversation, author *models.Account, visibility uint8, recipient *models.Account) *models.Status {
		t.Helper()
		now := time.Now()
		status := &models.Status{AccountId: author.Id, ConversationId: &conv.Id, Visibility: visibility, CreatedAt: &now, UpdatedAt: &now}
		require.NoError(t, conn.Create(status).Error)
		if recipient != nil {
			require.NoError(t, conn.Table("status_to_accounts").Create(map[string]any{"status_id": status.Id, "account_id": recipient.Id}).Error)
		}
		conv.AppendStatus(status.Id)
		require.NoError(t, testDB.GormDB.Save(conv).Error)
		return status
	}
	// A real DM followed by a public reply and a private message to somebody
	// else in the same reply thread must retain only Alice's visible DM preview.
	direct := createTestConversation(t, []uint64{alice.Id, bob.Id, carol.Id}, nil)
	visible := addStatus(direct, bob, models.StatusVisibilityDirect, alice)
	addStatus(direct, bob, models.StatusVisibilityPublic, nil)
	addStatus(direct, bob, models.StatusVisibilityDirect, carol)
	public := createTestConversation(t, []uint64{alice.Id, bob.Id}, nil)
	addStatus(public, bob, models.StatusVisibilityPublic, nil)
	private := createTestConversation(t, []uint64{alice.Id, bob.Id}, nil)
	addStatus(private, bob, models.StatusVisibilityPrivate, nil)
	hidden := createTestConversation(t, []uint64{alice.Id, bob.Id, carol.Id}, nil)
	addStatus(hidden, bob, models.StatusVisibilityDirect, carol)
	outsider := createTestConversation(t, []uint64{bob.Id, carol.Id}, nil)
	addStatus(outsider, bob, models.StatusVisibilityDirect, carol)
	deleted := createTestConversation(t, []uint64{alice.Id, bob.Id}, nil)
	gone := addStatus(deleted, bob, models.StatusVisibilityDirect, alice)
	require.NoError(t, conn.Model(gone).Update("deleted_at", time.Now()).Error)
	scheduled := createTestConversation(t, []uint64{alice.Id, bob.Id}, nil)
	future := addStatus(scheduled, bob, models.StatusVisibilityDirect, alice)
	require.NoError(t, conn.Model(future).Update("scheduled_at", time.Now().Add(time.Hour)).Error)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/conversations?limit=1", nil)
	c.Set("account", alice)
	callHandler(List, c)
	require.Empty(t, c.Errors)
	require.Equal(t, http.StatusOK, w.Code)
	var result []serializer.Conversation
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.Len(t, result, 1)
	require.Equal(t, strconv.FormatUint(direct.Id, 10), result[0].Id)
	require.NotNil(t, result[0].LastStatus)
	require.Equal(t, strconv.FormatUint(visible.Id, 10), result[0].LastStatus.Id)
	require.Equal(t, "direct", result[0].LastStatus.Visibility)
	participantIDs := func(c serializer.Conversation) []string {
		ids := make([]string, 0, len(c.Accounts))
		for _, account := range c.Accounts {
			ids = append(ids, account.Id)
		}
		return ids
	}
	require.ElementsMatch(t, []string{strconv.FormatUint(alice.Id, 10), strconv.FormatUint(bob.Id, 10)}, participantIDs(result[0]), "participants from an unreadable branch must remain private")
	// Reading only the visible branch must produce a real single response and
	// a later hidden branch must not change either preview or unread status.
	require.NoError(t, conn.Model(&models.AccountConversation{}).Where("conversation_id = ? AND account_id = ?", direct.Id, alice.Id).Update("last_status_id", visible.Id).Error)
	single, err := serializer.ToConversation(direct, alice)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatUint(direct.Id, 10), single.Id)
	require.False(t, single.Unread)
	require.Equal(t, strconv.FormatUint(visible.Id, 10), single.LastStatus.Id)
	require.ElementsMatch(t, participantIDs(result[0]), participantIDs(single))
	newVisible := addStatus(direct, bob, models.StatusVisibilityDirect, alice)
	single, err = serializer.ToConversation(direct, alice)
	require.NoError(t, err)
	require.True(t, single.Unread, "a new visible DM after reading must become unread again")
	require.Equal(t, strconv.FormatUint(newVisible.Id, 10), single.LastStatus.Id)
	// Filtering the private inbox must not alter public reply-thread storage.
	var preserved models.Conversation
	require.NoError(t, testDB.GormDB.First(&preserved, public.Id).Error)
	require.Len(t, preserved.StatusesIds, 1)
}
