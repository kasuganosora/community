package list

import (
	"strconv"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func tagStatus(t *testing.T, gdb *gorm.DB, s *models.Status, tag *models.Tag) {
	t.Helper()
	require.NoError(t, gdb.Create(&models.StatusTag{StatusId: s.Id, TagId: tag.Id}).Error)
}

func candidateIDs(t *testing.T, gdb *gorm.DB, l *models.List) []uint64 {
	t.Helper()
	var ids []uint64
	require.NoError(t, (&ListTimeline{List: l}).candidates(gdb.Model(&models.Status{})).Order("statuses.id").Pluck("statuses.id", &ids).Error)
	return ids
}

func TestListTimelineIncludesHashtagPosts(t *testing.T) {
	gdb := setupDBOnly(t)
	owner := createAccount(t, gdb, "owner")
	member := createAccount(t, gdb, "member")
	stranger := createAccount(t, gdb, "stranger")
	blocked := createAccount(t, gdb, "blocked")
	blocker := createAccount(t, gdb, "blocker")
	l := createList(t, gdb, owner, "godot")
	addListMember(t, gdb, l, member)
	godot := &models.Tag{Name: "godot"}
	other := &models.Tag{Name: "other"}
	require.NoError(t, gdb.Create(godot).Error)
	require.NoError(t, gdb.Create(other).Error)
	require.NoError(t, gdb.Create(&models.ListHashtag{ListId: l.Id, TagId: godot.Id}).Error)
	require.NoError(t, gdb.Create(&models.Block{AccountId: owner.Id, Type: models.BlockTypeAccount, TargetAccountId: blocked.Id}).Error)
	require.NoError(t, gdb.Create(&models.Block{AccountId: blocker.Id, Type: models.BlockTypeAccount, TargetAccountId: owner.Id}).Error)

	memberPost := createStatus(t, gdb, member)
	tagged := createStatus(t, gdb, stranger)
	tagStatus(t, gdb, tagged, godot)
	taggedUnlisted := createStatus(t, gdb, stranger, func(s *models.Status) { s.Visibility = models.StatusVisibilityUnlisted })
	tagStatus(t, gdb, taggedUnlisted, godot)
	untagged := createStatus(t, gdb, stranger)
	otherTag := createStatus(t, gdb, stranger)
	tagStatus(t, gdb, otherTag, other)
	private := createStatus(t, gdb, stranger, func(s *models.Status) { s.Visibility = models.StatusVisibilityPrivate })
	tagStatus(t, gdb, private, godot)
	fromBlocked := createStatus(t, gdb, blocked)
	tagStatus(t, gdb, fromBlocked, godot)
	fromBlocker := createStatus(t, gdb, blocker)
	tagStatus(t, gdb, fromBlocker, godot)
	both := createStatus(t, gdb, member) // member post that is also tagged: once
	tagStatus(t, gdb, both, godot)
	_ = untagged

	assert.Equal(t, []uint64{memberPost.Id, tagged.Id, taggedUnlisted.Id, both.Id}, candidateIDs(t, gdb, l))

	// replies_policy "none" also drops tagged replies.
	reply := createStatus(t, gdb, stranger)
	require.NoError(t, gdb.Model(&models.Status{}).Where("id = ?", reply.Id).UpdateColumns(map[string]any{"in_reply_to_id": tagged.Id, "in_reply_to_account_id": stranger.Id}).Error)
	tagStatus(t, gdb, reply, godot)
	assert.Contains(t, candidateIDs(t, gdb, l), reply.Id)
	l.RepliesPolicy = models.ListRepliesPolicyNone
	assert.NotContains(t, candidateIDs(t, gdb, l), reply.Id)
}

func TestPushReachesListsFollowingTheHashtag(t *testing.T) {
	gdb, mr := setupFull(t)
	owner := createAccount(t, gdb, "owner")
	stranger := createAccount(t, gdb, "stranger")
	tagged := createList(t, gdb, owner, "tagged")
	untouched := createList(t, gdb, owner, "untouched")
	godot := &models.Tag{Name: "godot"}
	require.NoError(t, gdb.Create(godot).Error)
	require.NoError(t, gdb.Create(&models.ListHashtag{ListId: tagged.Id, TagId: godot.Id}).Error)

	s := createStatus(t, gdb, stranger)
	tagStatus(t, gdb, s, godot)
	require.NoError(t, Push(s, false, false))
	assert.True(t, mr.Exists("feed:list:v2:"+strconv.FormatUint(tagged.Id, 10)))
	assert.False(t, mr.Exists("feed:list:v2:"+strconv.FormatUint(untouched.Id, 10)))

	// An owner who blocks the author does not get the post.
	blockedList := createList(t, gdb, stranger, "self")
	_ = blockedList
	other := createAccount(t, gdb, "other")
	ol := createList(t, gdb, other, "o")
	require.NoError(t, gdb.Create(&models.ListHashtag{ListId: ol.Id, TagId: godot.Id}).Error)
	require.NoError(t, gdb.Create(&models.Block{AccountId: other.Id, Type: models.BlockTypeAccount, TargetAccountId: stranger.Id}).Error)
	s2 := createStatus(t, gdb, stranger)
	tagStatus(t, gdb, s2, godot)
	require.NoError(t, Push(s2, false, false))
	assert.False(t, mr.Exists("feed:list:v2:"+strconv.FormatUint(ol.Id, 10)))
}
