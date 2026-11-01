package activitypub

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRecoverReplyParentUsesOriginAndDoesNotFetchParent(t *testing.T) {
	conn := sensitiveDB(t)
	now := time.Now()
	author := &models.Account{Username: "luna", DisplayName: "Luna", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(author).Error)
	parent := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: models.StatusContent{Content: "original post"}}
	require.NoError(t, conn.Create(parent).Error)
	origin := "https://remote.example/notes/reply-1"
	domain := "remote.example"
	reply := &models.Status{AccountId: author.Id, Domain: &domain, OriginUri: &origin, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: models.StatusContent{Content: "reply body"}}
	require.NoError(t, conn.Create(reply).Error)
	parentURI := systemsettings.GetBaseURL() + "/users/luna/statuses/" + strconv.FormatUint(parent.Id, 10)
	asked := []string{}
	linked, err := RecoverUnlinkedReplyParents(context.Background(), 10, func(_ context.Context, uri string) (map[string]any, error) {
		asked = append(asked, uri)
		if uri != origin {
			return nil, errNotLocalPoll
		}
		return map[string]any{"id": origin, "type": "Note", "inReplyTo": map[string]any{"type": "Note", "id": parentURI}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{origin}, asked)
	require.GreaterOrEqual(t, linked, 1)
	var saved models.Status
	require.NoError(t, conn.First(&saved, reply.Id).Error)
	require.NotNil(t, saved.InReplyToId)
	require.Equal(t, parent.Id, *saved.InReplyToId)
	require.NotNil(t, saved.InReplyToAccountId)
	require.Equal(t, author.Id, *saved.InReplyToAccountId)
}

// The newest unlinked replies are often unlinkable. A later page must move to
// older status ids instead of reading that same window again.
func TestReplyParentRepairSecondPageIsOlderThanTheFirst(t *testing.T) {
	conn := sensitiveDB(t)
	author := seedReplyRepairAuthor(t, conn)
	const (
		newest = uint64(404)
		older  = uint64(303)
		oldest = uint64(101)
	)
	seedUnlinkedRemoteReply(t, conn, author, 600, "")
	seedUnlinkedRemoteReply(t, conn, author, 550, "https://remote.example/notes/550")
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 550).UpdateColumn("deleted_at", time.Now()).Error)
	seedUnlinkedRemoteReply(t, conn, author, 500, "https://remote.example/notes/500")
	parent := uint64(1)
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 500).UpdateColumn("in_reply_to_id", parent).Error)
	seedUnlinkedRemoteReply(t, conn, author, 450, "https://remote.example/notes/450")
	reblog := uint64(500)
	require.NoError(t, conn.Model(&models.Status{}).Where("id = ?", 450).UpdateColumn("reblog_id", reblog).Error)
	for _, id := range []uint64{newest, older, 202, oldest} {
		seedUnlinkedRemoteReply(t, conn, author, id, "https://remote.example/notes/"+strconv.FormatUint(id, 10))
	}

	asked := []string{}
	fetch := unlinkableReplyFetch(&asked)
	first, err := RepairReplyParents(context.Background(), ReplyParentRepair{Limit: 2, MaxPages: 1}, fetch)
	require.NoError(t, err)
	require.Equal(t, []uint64{newest, older}, first.Examined)
	require.Equal(t, older, first.Cursor)
	require.False(t, first.Done)
	require.Equal(t, 0, first.Linked)
	require.Equal(t, []string{
		"https://remote.example/notes/404",
		"https://remote.example/notes/303",
	}, asked)

	second, err := RepairReplyParents(context.Background(), ReplyParentRepair{Limit: 2, AfterID: first.Cursor, MaxPages: 1}, fetch)
	require.NoError(t, err)
	require.Equal(t, []uint64{202, oldest}, second.Examined)
	assertOlderReplyPage(t, first.Examined, second.Examined)
	require.Equal(t, oldest, second.Cursor)
	require.NotEqual(t, first.Examined, second.Examined)

	third, err := RepairReplyParents(context.Background(), ReplyParentRepair{Limit: 2, AfterID: second.Cursor, MaxPages: 1}, fetch)
	require.NoError(t, err)
	require.Empty(t, third.Examined)
	require.True(t, third.Done)
	require.Equal(t, []string{
		"https://remote.example/notes/404",
		"https://remote.example/notes/303",
		"https://remote.example/notes/202",
		"https://remote.example/notes/101",
	}, asked)
}

func TestReplyParentRepairPagesPastUnlinkableNewest(t *testing.T) {
	conn := sensitiveDB(t)
	author := seedReplyRepairAuthor(t, conn)
	for _, id := range []uint64{404, 303, 202, 101} {
		seedUnlinkedRemoteReply(t, conn, author, id, "https://remote.example/notes/"+strconv.FormatUint(id, 10))
	}
	asked := []string{}
	result, err := RepairReplyParents(context.Background(), ReplyParentRepair{Limit: 2}, unlinkableReplyFetch(&asked))
	require.NoError(t, err)
	require.Equal(t, []uint64{404, 303, 202, 101}, result.Examined)
	assertOlderReplyPage(t, result.Examined[:2], result.Examined[2:])
	require.True(t, result.Done)
	require.Equal(t, uint64(101), result.Cursor)
	require.Len(t, asked, 4)
}

func seedReplyRepairAuthor(t *testing.T, conn *gorm.DB) uint64 {
	t.Helper()
	now := time.Now()
	author := &models.Account{Username: "luna", DisplayName: "Luna", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(author).Error)
	return author.Id
}

func seedUnlinkedRemoteReply(t *testing.T, conn *gorm.DB, author, id uint64, origin string) {
	t.Helper()
	now := time.Now()
	domain := "remote.example"
	reply := &models.Status{
		Id: id, AccountId: author, Domain: &domain, Visibility: models.StatusVisibilityPublic,
		CreatedAt: &now, UpdatedAt: &now, Content: models.StatusContent{Content: "reply"},
	}
	if origin != "" {
		reply.OriginUri = &origin
	}
	require.NoError(t, conn.Create(reply).Error)
	require.Equal(t, id, reply.Id)
}

func unlinkableReplyFetch(asked *[]string) func(context.Context, string) (map[string]any, error) {
	return func(_ context.Context, uri string) (map[string]any, error) {
		*asked = append(*asked, uri)
		if !strings.Contains(uri, "/notes/") {
			return nil, errNotLocalPoll
		}
		return map[string]any{"id": uri, "type": "Note", "inReplyTo": "https://remote.example/notes/missing-parent"}, nil
	}
}

func assertOlderReplyPage(t *testing.T, first, second []uint64) {
	t.Helper()
	require.NotEmpty(t, first)
	require.NotEmpty(t, second)
	minFirst := first[0]
	for _, id := range first {
		if id < minFirst {
			minFirst = id
		}
	}
	seen := map[uint64]bool{}
	for _, id := range first {
		seen[id] = true
	}
	for _, id := range second {
		require.Less(t, id, minFirst)
		require.False(t, seen[id])
	}
}
