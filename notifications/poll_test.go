package notifications

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// finishPollNotification
// ---------------------------------------------------------------------------

func TestFinishPollNotification_NotExpiredSkips(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := createStatus(t, gdb, author.Id)

	future := time.Now().Add(24 * time.Hour)
	poll := &models.Poll{
		StatusId:  status.Id,
		AccountId: author.Id,
		ExpiresAt: &future,
	}
	require.NoError(t, gdb.Create(poll).Error)

	voter := createLocalAccount(t, gdb, "voter")
	require.NoError(t, gdb.Create(&models.PollOptionVote{PollId: poll.Id, AccountId: voter.Id}).Error)

	before := countNotifications(t, gdb, voter.Id)

	finishPollNotification(poll)

	assert.Equal(t, before, countNotifications(t, gdb, voter.Id),
		"a poll that has not expired must not send notifications")
}

func TestFinishPollNotification_ExpiredNotifiesLocalVoters(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := createStatus(t, gdb, author.Id)

	past := time.Now().Add(-1 * time.Hour)
	poll := &models.Poll{
		StatusId:  status.Id,
		AccountId: author.Id,
		ExpiresAt: &past,
	}
	require.NoError(t, gdb.Create(poll).Error)

	voter1 := createLocalAccount(t, gdb, "voter1")
	voter2 := createLocalAccount(t, gdb, "voter2")
	require.NoError(t, gdb.Create(&models.PollOptionVote{PollId: poll.Id, AccountId: voter1.Id}).Error)
	require.NoError(t, gdb.Create(&models.PollOptionVote{PollId: poll.Id, AccountId: voter2.Id}).Error)

	finishPollNotification(poll)

	for _, voter := range []*models.Account{voter1, voter2} {
		ns := allNotifications(t, gdb, voter.Id, models.NotificationTypePoll)
		require.Len(t, ns, 1, "each local voter should receive exactly one poll notification")
		assert.Equal(t, status.Id, ns[0].StatusId)
		assert.Equal(t, author.Id, ns[0].FromAccountId)
		assert.Equal(t, voter.Id, ns[0].AccountId)
	}
}

func TestFinishPollNotification_DedupsVotersMultipleOptions(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := createStatus(t, gdb, author.Id)

	past := time.Now().Add(-1 * time.Hour)
	poll := &models.Poll{
		StatusId:  status.Id,
		AccountId: author.Id,
		ExpiresAt: &past,
		Multiple:  true,
	}
	require.NoError(t, gdb.Create(poll).Error)

	voter := createLocalAccount(t, gdb, "voter")
	// Voter casts multiple votes -> must still get a single notification.
	require.NoError(t, gdb.Create(&models.PollOptionVote{PollId: poll.Id, AccountId: voter.Id}).Error)
	require.NoError(t, gdb.Create(&models.PollOptionVote{PollId: poll.Id, AccountId: voter.Id}).Error)
	require.NoError(t, gdb.Create(&models.PollOptionVote{PollId: poll.Id, AccountId: voter.Id}).Error)

	finishPollNotification(poll)

	ns := allNotifications(t, gdb, voter.Id, models.NotificationTypePoll)
	assert.Len(t, ns, 1, "duplicate voter IDs must be deduplicated to a single notification")
}

func TestFinishPollNotification_SkipsRemoteVoters(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := createStatus(t, gdb, author.Id)

	past := time.Now().Add(-1 * time.Hour)
	poll := &models.Poll{
		StatusId:  status.Id,
		AccountId: author.Id,
		ExpiresAt: &past,
	}
	require.NoError(t, gdb.Create(poll).Error)

	remoteVoter := createRemoteAccount(t, gdb, "remotevoter", "fed.host")
	require.NoError(t, gdb.Create(&models.PollOptionVote{PollId: poll.Id, AccountId: remoteVoter.Id}).Error)

	finishPollNotification(poll)

	ns := allNotifications(t, gdb, remoteVoter.Id, models.NotificationTypePoll)
	assert.Empty(t, ns, "remote voters must not receive poll notifications")
}

func TestFinishPollNotification_NoVotesSendsNothing(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := createStatus(t, gdb, author.Id)

	past := time.Now().Add(-1 * time.Hour)
	poll := &models.Poll{
		StatusId:  status.Id,
		AccountId: author.Id,
		ExpiresAt: &past,
	}
	require.NoError(t, gdb.Create(poll).Error)

	// No votes at all. The empty-batch guard must avoid calling Create.
	assert.NotPanics(t, func() {
		finishPollNotification(poll)
	})

	var total int64
	require.NoError(t, gdb.Model(&models.Notification{}).Where("type = ?", models.NotificationTypePoll).Count(&total).Error)
	assert.Equal(t, int64(0), total, "no votes -> no poll notifications")
}

func TestFinishPollNotification_StatusNotFoundSkips(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	voter := createLocalAccount(t, gdb, "voter")

	past := time.Now().Add(-1 * time.Hour)
	poll := &models.Poll{
		StatusId:  999999, // non-existent status
		AccountId: voter.Id,
		ExpiresAt: &past,
	}
	require.NoError(t, gdb.Create(poll).Error)
	require.NoError(t, gdb.Create(&models.PollOptionVote{PollId: poll.Id, AccountId: voter.Id}).Error)

	before := countNotifications(t, gdb, voter.Id)

	// Status lookup returns ErrRecordNotFound -> function returns early.
	assert.NotPanics(t, func() {
		finishPollNotification(poll)
	})

	assert.Equal(t, before, countNotifications(t, gdb, voter.Id),
		"if the status cannot be found, no notification should be sent")
}

// ---------------------------------------------------------------------------
// Event-handler routing (poll "finish")
// ---------------------------------------------------------------------------

func TestPollEvent_NonPollDataIgnored(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)

	// Handler with wrong data type should not panic.
	require.NotPanics(t, func() {
		event.Trigger("poll", event.Event{
			Name: "finish",
			Data: "not a poll",
		})
	})
}
