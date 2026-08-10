package notifications

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// followNotification
// ---------------------------------------------------------------------------

func TestFollowNotification_SelfFollowNoNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	target := createLocalAccount(t, gdb, "target")

	before := countNotifications(t, gdb, target.Id)

	// AccountId == FollowId -> self-follow, must be skipped before any DB work.
	followNotification(&models.Follow{
		AccountId: target.Id,
		FollowId:  target.Id,
	})

	// No notification should have been created.
	assert.Equal(t, before, countNotifications(t, gdb, target.Id),
		"self-follow must not produce a notification")
}

func TestFollowNotification_CreatesNotificationForLocal(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")     // FollowId (被关注者)
	follower := createLocalAccount(t, gdb, "follower") // AccountId (关注者)

	followNotification(&models.Follow{
		AccountId: follower.Id,
		FollowId:  author.Id,
	})

	nf := latestNotificationFor(t, gdb, author.Id, models.NotificationTypeFollow)
	require.NotNil(t, nf, "a follow notification should have been created for the local author")
	assert.Equal(t, uint8(models.NotificationTypeFollow), nf.Type)
	assert.Equal(t, follower.Id, nf.FromAccountId)
	assert.Equal(t, author.Id, nf.AccountId)
}

func TestFollowNotification_SkipsRemoteTarget(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	remote := createRemoteAccount(t, gdb, "remote", "example.com")
	follower := createLocalAccount(t, gdb, "follower")

	before := countNotifications(t, gdb, remote.Id)

	followNotification(&models.Follow{
		AccountId: follower.Id,
		FollowId:  remote.Id,
	})

	// The followee is remote -> no notification.
	assert.Equal(t, before, countNotifications(t, gdb, remote.Id),
		"follow notification must not be created for a remote target")
}

func TestFollowNotification_DedupNoSecondNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	follower := createLocalAccount(t, gdb, "follower")

	// First call creates one notification.
	followNotification(&models.Follow{AccountId: follower.Id, FollowId: author.Id})
	require.Equal(t, int64(1), countNotifications(t, gdb, author.Id))

	// Second call must be deduplicated (count > 0 guard).
	followNotification(&models.Follow{AccountId: follower.Id, FollowId: author.Id})
	assert.Equal(t, int64(1), countNotifications(t, gdb, author.Id),
		"duplicate follow notification must not be created")
}

func TestFollowNotification_NonExistentTargetNoNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	follower := createLocalAccount(t, gdb, "follower")

	// FollowId points at an account that does not exist.
	// isLocalAccount returns (false, nil) for ErrRecordNotFound, so no notification.
	followNotification(&models.Follow{AccountId: follower.Id, FollowId: 999999})

	var n int64
	require.NoError(t, gdb.Model(&models.Notification{}).Where("account_id = ?", 999999).Count(&n).Error)
	assert.Equal(t, int64(0), n, "no notification for a non-existent target")
}

// ---------------------------------------------------------------------------
// followRequestNotification
// ---------------------------------------------------------------------------

func TestFollowRequestNotification_SelfRequestNoNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	acc := createLocalAccount(t, gdb, "acc")

	before := countNotifications(t, gdb, acc.Id)

	followRequestNotification(&models.Follow{AccountId: acc.Id, FollowId: acc.Id})

	assert.Equal(t, before, countNotifications(t, gdb, acc.Id),
		"self follow-request must not create a notification")
}

func TestFollowRequestNotification_CreatesNotificationForLocal(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	target := createLocalAccount(t, gdb, "target")
	requester := createLocalAccount(t, gdb, "requester")

	followRequestNotification(&models.Follow{AccountId: requester.Id, FollowId: target.Id})

	nf := latestNotificationFor(t, gdb, target.Id, models.NotificationTypeFollowRequest)
	require.NotNil(t, nf, "follow_request notification should be created")
	assert.Equal(t, uint8(models.NotificationTypeFollowRequest), nf.Type)
	assert.Equal(t, requester.Id, nf.FromAccountId)
	assert.Equal(t, target.Id, nf.AccountId)
}

func TestFollowRequestNotification_SkipsRemoteTarget(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	remote := createRemoteAccount(t, gdb, "remote", "social.example")
	requester := createLocalAccount(t, gdb, "requester")

	before := countNotifications(t, gdb, remote.Id)

	followRequestNotification(&models.Follow{AccountId: requester.Id, FollowId: remote.Id})

	assert.Equal(t, before, countNotifications(t, gdb, remote.Id),
		"follow_request notification must not be created for a remote target")
}

// ---------------------------------------------------------------------------
// isLocalAccount
// ---------------------------------------------------------------------------

func TestIsLocalAccount_ZeroIdShortCircuits(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)

	// Zero id short-circuits without touching the DB.
	local, err := isLocalAccount(0)
	require.NoError(t, err)
	assert.False(t, local, "accountId 0 must not be considered local")
}

func TestIsLocalAccount_LocalAccount(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	acc := createLocalAccount(t, gdb, "localuser")

	local, err := isLocalAccount(acc.Id)
	require.NoError(t, err)
	assert.True(t, local, "account with Domain == nil should be local")
}

func TestIsLocalAccount_RemoteAccount(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	acc := createRemoteAccount(t, gdb, "remoteuser", "fed.host")

	local, err := isLocalAccount(acc.Id)
	require.NoError(t, err)
	assert.False(t, local, "account with Domain != nil should not be local")
}

func TestIsLocalAccount_NotFound(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)

	local, err := isLocalAccount(888888)
	require.NoError(t, err, "missing account should map to (false, nil), not an error")
	assert.False(t, local)
}
