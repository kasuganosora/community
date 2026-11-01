package models

import (
	"strconv"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func replyParentDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&Account{}, &AccountExtra{}, &Status{}, &StatusContent{}, &StatusToAccount{}))
	return conn
}

func TestFindReplyParentLocalForms(t *testing.T) {
	conn := replyParentDB(t)
	now := time.Now()
	base := "https://social.example"
	actor := base + "/users/luna"
	author := &Account{Id: 20, Username: "luna", DisplayName: "Luna", LocalActorURI: actor, CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(author).Error)
	parent := &Status{Id: 200, AccountId: author.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: "original post"}}
	require.NoError(t, conn.Create(parent).Error)
	id := strconv.FormatUint(parent.Id, 10)
	for _, uri := range []string{
		base + "/users/luna/statuses/" + id,
		base + "/@luna/status/" + id,
		base + "/@luna/" + id,
		actor + "/statuses/" + id,
	} {
		found, err := FindReplyParent(conn, base, uri)
		require.NoError(t, err, uri)
		require.Equal(t, parent.Id, found.Id, uri)
	}
	_, err := FindReplyParent(conn, base, "https://remote.example/notes/missing")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestLinkStoredReplyParentFromURIOrBody(t *testing.T) {
	conn := replyParentDB(t)
	now := time.Now()
	base := "https://social.example"
	author := &Account{Id: 20, Username: "luna", CreatedAt: &now, UpdatedAt: &now}
	replier := &Account{Id: 21, Username: "sky", Domain: strPtr("remote.example"), CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(author).Error)
	require.NoError(t, conn.Omit("Extra").Create(replier).Error)
	parent := &Status{Id: 200, AccountId: author.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: "original post"}}
	require.NoError(t, conn.Create(parent).Error)
	other := &Status{Id: 199, AccountId: author.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: "other post"}}
	require.NoError(t, conn.Create(other).Error)
	uri := base + "/@luna/status/" + strconv.FormatUint(parent.Id, 10)
	otherURI := base + "/@luna/status/" + strconv.FormatUint(other.Id, 10)
	stored := &Status{Id: 201, AccountId: replier.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Properties: map[string]any{StatusPropReplyToURI: uri}, Content: StatusContent{Content: "reply"}}
	require.NoError(t, conn.Create(stored).Error)
	body := &Status{Id: 202, AccountId: replier.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: `<p><a href="` + uri + `">post</a> reply</p>`}}
	require.NoError(t, conn.Create(body).Error)
	ambiguous := &Status{Id: 203, AccountId: replier.Id, Visibility: StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: StatusContent{Content: uri + " " + otherURI}}
	require.NoError(t, conn.Create(ambiguous).Error)

	require.NoError(t, LinkStoredReplyParents(conn, []*Status{stored, body, ambiguous}, base))
	var saved, fromBody, fromAmbiguous Status
	require.NoError(t, conn.First(&saved, stored.Id).Error)
	require.NotNil(t, saved.InReplyToId)
	require.Equal(t, parent.Id, *saved.InReplyToId)
	require.NotNil(t, saved.InReplyToAccountId)
	require.Equal(t, author.Id, *saved.InReplyToAccountId)
	require.Empty(t, ReplyToURI(&saved))
	require.NoError(t, conn.First(&fromBody, body.Id).Error)
	require.NotNil(t, fromBody.InReplyToId)
	require.Equal(t, parent.Id, *fromBody.InReplyToId)
	require.NoError(t, conn.First(&fromAmbiguous, ambiguous.Id).Error)
	require.Nil(t, fromAmbiguous.InReplyToId)

	again, err := RepairUnlinkedReplyParents(conn, base)
	require.NoError(t, err)
	require.Equal(t, 0, again)
}

func strPtr(value string) *string { return &value }
