package serializer

import (
	"context"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type timelineSQLCounter struct {
	logger.Interface
	queries []string
	rows    int64
}

func (c *timelineSQLCounter) Trace(_ context.Context, _ time.Time, sql func() (string, int64), _ error) {
	query, rows := sql()
	c.queries = append(c.queries, query)
	c.rows += rows
}
func timelineBatchDB(t *testing.T) *gorm.DB {
	t.Helper()
	withTestDBMigrated(t, &models.Account{}, &models.Status{}, &models.StatusContent{}, &models.Block{}, &models.Follow{}, &models.StatusToAccount{}, &models.Filter{}, &models.MuteAccount{}, &models.MuteStatus{}, &models.PinStatus{}, &models.FavoriteStatus{}, &models.BookmarkStatus{}, &models.PollOptionVote{}, &models.StatusReaction{}, &models.Emoji{}, &models.AccountEmoji{}, &models.SystemSetting{})
	return db.GetDB().Session(&gorm.Session{SkipHooks: true})
}
func batchStatus(id uint64, author *models.Account, visibility uint8) *models.Status {
	now := time.Now()
	return &models.Status{Id: id, AccountId: author.Id, Account: author, CreatedAt: &now, Visibility: visibility, Content: models.StatusContent{Content: "a visible post"}, ToAccounts: []*models.Account{}}
}
func TestTimelineBatchQueryBudgetAndEmojiRefresh(t *testing.T) {
	conn := timelineBatchDB(t)
	local := &models.Account{Id: 1, Username: "local", DisplayName: "Local :wave:"}
	domain := "remote.example"
	remote := &models.Account{Id: 2, Username: "remote", Domain: &domain, DisplayName: "Remote :wave:"}
	viewer := &models.Account{Id: 3, Username: "viewer"}
	localEmoji := &models.Emoji{Id: 10, Shortcode: "wave", Url: "https://local/wave.png", StaticUrl: "https://local/wave.png"}
	remoteEmoji := &models.Emoji{Id: 11, Shortcode: "wave", Domain: domain, Url: "https://remote/wave.png", StaticUrl: "https://remote/wave.png"}
	require.NoError(t, conn.Create(localEmoji).Error)
	require.NoError(t, conn.Create(remoteEmoji).Error)
	require.NoError(t, conn.Create(&models.AccountEmoji{AccountId: 2, EmojiId: 11}).Error)
	_ = contentRenderOptions()
	counter := &timelineSQLCounter{Interface: logger.Discard}
	original := db.GetDB().Logger
	db.GetDB().Logger = counter
	defer func() { db.GetDB().Logger = original }()
	counts := []int{}
	for _, size := range []int{20, 80} {
		statuses := make(models.Statuses, 0, size)
		for i := 0; i < size; i++ {
			author := local
			if i%2 == 1 {
				author = remote
			}
			statuses = append(statuses, batchStatus(uint64(i+1), author, models.StatusVisibilityPublic))
		}
		counter.queries = nil
		out, err := ToStatuses(statuses, viewer)
		require.NoError(t, err)
		require.Len(t, out, size)
		require.Len(t, out[0].Account.Emoji, 1)
		require.Equal(t, localEmoji.Url, out[0].Account.Emoji[0].Url)
		require.Len(t, out[1].Account.Emoji, 1)
		require.Equal(t, remoteEmoji.Url, out[1].Account.Emoji[0].Url)
		counts = append(counts, len(counter.queries))
		require.LessOrEqual(t, len(counter.queries), 8, "whole-page database round trips must remain bounded")
	}
	require.Equal(t, counts[0], counts[1], "quadrupling page size must not quadruple serializer queries")
	// Request-local caches must not conceal an administrative disable on next read.
	require.NoError(t, conn.Model(localEmoji).UpdateColumn("disabled", true).Error)
	out, err := ToStatuses(models.Statuses{batchStatus(99, local, models.StatusVisibilityPublic)}, viewer)
	require.NoError(t, err)
	require.Empty(t, out[0].Account.Emoji)
	t.Logf("20 posts=%d SQL queries; 80 posts=%d SQL queries", counts[0], counts[1])
}
func TestTimelineBatchReblogsKeepViewerMetadataAndPrivacy(t *testing.T) {
	conn := timelineBatchDB(t)
	author := &models.Account{Id: 1, Username: "author"}
	booster := &models.Account{Id: 2, Username: "booster"}
	viewer := &models.Account{Id: 3, Username: "viewer"}
	original := batchStatus(10, author, models.StatusVisibilityPublic)
	original.Content.Content = "needle"
	original.Poll = &models.Poll{Id: 70, StatusId: 10, Options: models.PollOptions{{Id: 700, Index: 0, Content: "yes"}, {Id: 701, Index: 1, Content: "no"}}}
	wrapper := batchStatus(20, booster, models.StatusVisibilityPublic)
	wrapper.ReblogId = &original.Id
	wrapper.Reblog = original
	require.NoError(t, conn.Create(&models.FavoriteStatus{AccountId: viewer.Id, StatusId: original.Id}).Error)
	require.NoError(t, conn.Create(&models.BookmarkStatus{AccountId: viewer.Id, StatusId: original.Id}).Error)
	require.NoError(t, conn.Create(&models.PinStatus{AccountId: viewer.Id, StatusId: original.Id}).Error)
	require.NoError(t, conn.Create(&models.PollOptionVote{PollId: 70, AccountId: viewer.Id, OptionId: 701}).Error)
	require.NoError(t, conn.Create(&models.StatusReaction{AccountID: viewer.Id, StatusID: 10, Name: "😀"}).Error)
	expiry := time.Now().Add(time.Hour)
	require.NoError(t, conn.Create(&models.Filter{AccountId: viewer.Id, Title: "needle", ExpiresAt: &expiry, Context: []string{"home"}, FilterAction: models.FilterActionWarn, Keywords: []*models.FilterKeyword{{Keyword: "needle"}}}).Error)
	out, err := ToStatuses(models.Statuses{wrapper}, viewer)
	require.NoError(t, err)
	require.Len(t, out, 1)
	nested := out[0].Reblog
	require.NotNil(t, nested)
	require.True(t, nested.Favourited)
	require.True(t, nested.Bookmarked)
	require.True(t, nested.Pinned)
	require.NotNil(t, nested.Poll)
	require.Equal(t, []int{1}, nested.Poll.OwnVotes)
	require.True(t, nested.EmojiReactions[0].Me)
	require.Equal(t, []string{"warn"}, nested.Filtered)
	require.False(t, out[0].Favourited, "a boost wrapper is not the original favourite")
	original.Visibility = models.StatusVisibilityDirect
	out, err = ToStatuses(models.Statuses{wrapper}, viewer)
	require.NoError(t, err)
	require.Empty(t, out, "public wrapper cannot expose an unauthorized original")
	original.ToAccounts = []*models.Account{viewer}
	out, err = ToStatuses(models.Statuses{wrapper}, viewer)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.NoError(t, conn.Create(&models.Block{AccountId: author.Id, TargetAccountId: viewer.Id}).Error)
	out, err = ToStatuses(models.Statuses{wrapper}, viewer)
	require.NoError(t, err)
	require.Empty(t, out, "reverse author block revokes a previously granted direct post")
}
func TestTimelinePollAggregationDoesNotLoadOtherPeoplesVotes(t *testing.T) {
	conn := timelineBatchDB(t)
	poll := &models.Poll{Id: 9, StatusId: 100, Options: models.PollOptions{{Id: 91, Index: 0, Content: "A"}, {Id: 92, Index: 1, Content: "B"}}}
	votes := make([]models.PollOptionVote, 0, 1002)
	for i := 0; i < 1000; i++ {
		votes = append(votes, models.PollOptionVote{PollId: 9, AccountId: uint64(i + 10), OptionId: 91})
	}
	votes = append(votes, models.PollOptionVote{PollId: 9, AccountId: 2, OptionId: 92}, models.PollOptionVote{PollId: 9, AccountId: 2, OptionId: 91})
	require.NoError(t, conn.CreateInBatches(votes, 100).Error)
	counter := &timelineSQLCounter{Interface: logger.Discard}
	original := db.GetDB().Logger
	db.GetDB().Logger = counter
	defer func() { db.GetDB().Logger = original }()
	out, err := ToPollsMapByStatusId([]*models.Poll{poll}, &models.Account{Id: 2})
	require.NoError(t, err)
	require.EqualValues(t, 1002, out[100].VotesCount)
	require.EqualValues(t, 1001, out[100].VotersCount)
	require.Equal(t, []int{1, 0}, out[100].OwnVotes)
	require.LessOrEqual(t, len(counter.queries), 2)
	require.LessOrEqual(t, counter.rows, int64(3), "one aggregate row and only the viewer's two votes should cross the DB boundary")
	counter.queries = nil
	_, err = ToPollsMapByStatusId(nil, nil)
	require.NoError(t, err)
	require.Empty(t, counter.queries)
}

func TestTimelineVisibilityBatchRechecksFollowsMentionsBlocksAndBans(t *testing.T) {
	conn := timelineBatchDB(t)
	author := &models.Account{Id: 1, Username: "author"}
	viewer := &models.Account{Id: 2, Username: "viewer"}
	banned := &models.Account{Id: 3, Username: "banned", IsBan: true}
	pub := batchStatus(1, author, models.StatusVisibilityPublic)
	private := batchStatus(2, author, models.StatusVisibilityPrivate)
	direct := batchStatus(3, author, models.StatusVisibilityDirect)
	direct.ToAccounts = nil
	unlisted := batchStatus(4, author, models.StatusVisibilityUnlisted)
	scheduled := batchStatus(5, author, models.StatusVisibilityPublic)
	now := time.Now()
	scheduled.ScheduledAt = &now
	own := batchStatus(6, viewer, models.StatusVisibilityDirect)
	deleted := batchStatus(7, author, models.StatusVisibilityPublic)
	deleted.DeletedAt = &now
	ban := batchStatus(8, banned, models.StatusVisibilityPublic)
	wrapper := batchStatus(9, viewer, models.StatusVisibilityPublic)
	wrapper.ReblogId = &private.Id
	wrapper.Reblog = private
	statuses := models.Statuses{pub, private, direct, unlisted, scheduled, own, deleted, ban, wrapper}
	ids := func(account *models.Account) []uint64 {
		visible, err := models.FilterVisibleStatuses(conn, account, statuses)
		require.NoError(t, err)
		return visible.GetIds()
	}
	require.Equal(t, []uint64{1, 4}, ids(nil))
	require.NoError(t, conn.Create(&models.StatusToAccount{StatusId: 3, AccountId: viewer.Id}).Error)
	require.Equal(t, []uint64{1, 3, 4, 6}, ids(viewer))
	follow := &models.Follow{Id: 99, AccountId: viewer.Id, FollowId: author.Id, Status: models.FollowStatusRequested}
	require.NoError(t, conn.Create(follow).Error)
	require.Equal(t, []uint64{1, 3, 4, 6}, ids(viewer), "pending request does not grant a private post")
	require.NoError(t, conn.Model(follow).UpdateColumn("status", models.FollowStatusFollowed).Error)
	require.Equal(t, []uint64{1, 2, 3, 4, 6, 9}, ids(viewer))
	require.NoError(t, conn.Delete(follow).Error)
	require.Equal(t, []uint64{1, 3, 4, 6}, ids(viewer), "unfollow must revoke both original and public wrapper")
	require.NoError(t, conn.Where("status_id = ?", 3).Delete(&models.StatusToAccount{}).Error)
	require.Equal(t, []uint64{1, 4, 6}, ids(viewer), "direct recipient removal must apply on the next read")
	block := &models.Block{AccountId: author.Id, TargetAccountId: viewer.Id}
	require.NoError(t, conn.Create(block).Error)
	require.Equal(t, []uint64{6}, ids(viewer), "author-to-viewer blocks apply in reverse direction")
	require.NoError(t, conn.Delete(block).Error)
	require.NoError(t, conn.Create(&models.Block{AccountId: 0, TargetAccountId: author.Id}).Error)
	require.Empty(t, ids(nil), "anonymous readers must respect global account blocks")
	domain := "blocked.example"
	remote := &models.Account{Id: 4, Username: "remote", Domain: &domain}
	statuses = models.Statuses{batchStatus(10, remote, models.StatusVisibilityPublic)}
	require.NoError(t, conn.Create(&models.Block{AccountId: 0, Type: models.BlockTypeDomain, TargetDomain: domain}).Error)
	require.Empty(t, ids(nil), "anonymous readers must respect global domain blocks")
}

func TestTimelineSerializationHonorsCancellationBeforeAndDuringQueries(t *testing.T) {
	conn := timelineBatchDB(t)
	author := &models.Account{Id: 1, Username: "author", DisplayName: "Author :wave:"}
	viewer := &models.Account{Id: 2, Username: "viewer"}
	status := batchStatus(10, author, models.StatusVisibilityPublic)
	require.NoError(t, conn.Create(&models.Emoji{Shortcode: "wave", Url: "https://example/wave.png", StaticUrl: "https://example/wave.png"}).Error)
	_ = contentRenderOptions()
	counter := &timelineSQLCounter{Interface: logger.Discard}
	original := db.GetDB().Logger
	db.GetDB().Logger = counter
	defer func() { db.GetDB().Logger = original }()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ToStatusesContext(cancelled, models.Statuses{status}, viewer)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, counter.queries, "an already cancelled request must not begin SQL work")
	type requestKey struct{}
	active, cancelActive := context.WithCancel(context.WithValue(context.Background(), requestKey{}, true))
	defer cancelActive()
	queriesAfterCancel := 0
	inheritedContext := true
	require.NoError(t, conn.Callback().Query().Before("gorm:query").Register("test_request_context", func(tx *gorm.DB) {
		if tx.Statement.Context.Value(requestKey{}) != true {
			inheritedContext = false
		}
		if active.Err() != nil {
			queriesAfterCancel++
		}
	}))
	defer conn.Callback().Query().Remove("test_request_context")
	require.NoError(t, conn.Callback().Query().After("gorm:query").Register("test_cancel_after_profile_emoji", func(tx *gorm.DB) {
		if tx.Statement.Table == "emojis" {
			cancelActive()
		}
	}))
	defer conn.Callback().Query().Remove("test_cancel_after_profile_emoji")
	_, err = ToStatusesContext(active, models.Statuses{status}, viewer)
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, inheritedContext, "profile metadata and visibility queries must inherit the request context")
	require.Zero(t, queriesAfterCancel, "do not start reaction/filter/poll work after cancellation")
}

func TestTimelineSerializerDatabaseHelpersPreserveCancelledContext(t *testing.T) {
	conn := timelineBatchDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	conn = conn.WithContext(ctx)
	counter := &timelineSQLCounter{Interface: logger.Discard}
	conn.Logger = counter
	_, err := newAccountSerializationWithDB(conn, []*models.Account{{Id: 1, DisplayName: ":wave:"}})
	require.ErrorIs(t, err, context.Canceled)
	_, err = statusReactionsBatchWithDB(conn, []uint64{1}, nil)
	require.ErrorIs(t, err, context.Canceled)
	_, err = toPollsMapByStatusIdWithDB(conn, []*models.Poll{{Id: 1, StatusId: 1}}, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, counter.queries, "none of the helpers may silently fall back to the global uncancelled database")
}

func TestTimelineViewerFlagsRemainIndependentAndRespectMuteExpiry(t *testing.T) {
	conn := timelineBatchDB(t)
	viewer := &models.Account{Id: 1, Username: "reader"}
	author := &models.Account{Id: 2, Username: "author"}
	expiredAuthor := &models.Account{Id: 3, Username: "expired"}
	activeAuthor := &models.Account{Id: 4, Username: "active"}
	posts := models.Statuses{batchStatus(10, author, 0), batchStatus(11, expiredAuthor, 0), batchStatus(12, activeAuthor, 0)}
	require.NoError(t, conn.Create(&models.PinStatus{AccountId: 1, StatusId: 10}).Error)
	require.NoError(t, conn.Create(&models.FavoriteStatus{AccountId: 1, StatusId: 10}).Error)
	require.NoError(t, conn.Create(&models.BookmarkStatus{AccountId: 1, StatusId: 10}).Error)
	require.NoError(t, conn.Create(&models.MuteStatus{AccountId: 1, StatusId: 10}).Error)
	boost := batchStatus(99, viewer, 0)
	boost.ReblogId = &posts[0].Id
	require.NoError(t, conn.Omit("Account", "Content", "ToAccounts").Create(boost).Error)
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	require.NoError(t, conn.Create(&models.MuteAccount{AccountId: 1, TargetId: 3, ExpiredAt: &past}).Error)
	require.NoError(t, conn.Create(&models.MuteAccount{AccountId: 1, TargetId: 4, ExpiredAt: &future}).Error)
	// Another user's interactions must never leak into the current viewer's flags.
	require.NoError(t, conn.Create(&models.FavoriteStatus{AccountId: 9, StatusId: 11}).Error)
	out, err := ToStatuses(posts, viewer)
	require.NoError(t, err)
	require.Len(t, out, 3)
	require.True(t, out[0].Pinned)
	require.True(t, out[0].Favourited)
	require.True(t, out[0].Reblogged)
	require.True(t, out[0].Bookmarked)
	require.True(t, out[0].Muted)
	require.False(t, out[1].Muted)
	require.False(t, out[1].Favourited)
	require.True(t, out[2].Muted)
	other, err := ToStatuses(posts, &models.Account{Id: 8, Username: "other"})
	require.NoError(t, err)
	for _, p := range other {
		require.False(t, p.Pinned)
		require.False(t, p.Favourited)
		require.False(t, p.Reblogged)
		require.False(t, p.Bookmarked)
		require.False(t, p.Muted)
	}
	require.NoError(t, conn.Model(&models.MuteAccount{}).Where("account_id = ? AND target_id = ?", 1, 4).Update("expired_at", nil).Error)
	out, err = ToStatuses(posts, viewer)
	require.NoError(t, err)
	require.True(t, out[2].Muted, "a permanent account mute remains active")
}
