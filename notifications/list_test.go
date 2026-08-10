package notifications

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// listAddToListNotification
// ---------------------------------------------------------------------------

func TestListAddToListNotification_CreatesForLocalMember(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	owner := createLocalAccount(t, gdb, "owner")
	member := createLocalAccount(t, gdb, "member")

	list := &models.List{Title: "my list", AccountId: owner.Id}
	require.NoError(t, gdb.Create(list).Error)

	listAddToListNotification(&models.ListAccount{ListId: list.Id, AccountId: member.Id})

	nf := latestNotificationFor(t, gdb, member.Id, models.NotificationTypeAddList)
	require.NotNil(t, nf, "an add_list notification should be created for the local member")
	assert.Equal(t, uint8(models.NotificationTypeAddList), nf.Type)
	assert.Equal(t, list.Id, nf.ListId)
	assert.Equal(t, owner.Id, nf.FromAccountId)
	assert.Equal(t, member.Id, nf.AccountId)
}

func TestListAddToListNotification_SkipsRemoteMember(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	owner := createLocalAccount(t, gdb, "owner")
	remoteMember := createRemoteAccount(t, gdb, "remote", "social.example")

	list := &models.List{Title: "list", AccountId: owner.Id}
	require.NoError(t, gdb.Create(list).Error)

	before := countNotifications(t, gdb, remoteMember.Id)

	listAddToListNotification(&models.ListAccount{ListId: list.Id, AccountId: remoteMember.Id})

	assert.Equal(t, before, countNotifications(t, gdb, remoteMember.Id),
		"a remote member must not receive an add_list notification")
}

func TestListAddToListNotification_ListNotFoundSkips(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	member := createLocalAccount(t, gdb, "member")

	before := countNotifications(t, gdb, member.Id)

	// List does not exist -> ErrRecordNotFound path returns early.
	listAddToListNotification(&models.ListAccount{ListId: 999999, AccountId: member.Id})

	assert.Equal(t, before, countNotifications(t, gdb, member.Id),
		"if the list cannot be found, no notification should be sent")
}

func TestListAddToListNotification_AccountNotFoundSkips(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	owner := createLocalAccount(t, gdb, "owner")

	list := &models.List{Title: "list", AccountId: owner.Id}
	require.NoError(t, gdb.Create(list).Error)

	var total int64
	listAddToListNotification(&models.ListAccount{ListId: list.Id, AccountId: 999999})
	require.NoError(t, gdb.Model(&models.Notification{}).Where("type = ?", models.NotificationTypeAddList).Count(&total).Error)
	assert.Equal(t, int64(0), total, "if the added account cannot be found, no notification is created")
}

// ---------------------------------------------------------------------------
// listFollowListNotification
// ---------------------------------------------------------------------------

func TestListFollowListNotification_CreatesForLocalCreator(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	owner := createLocalAccount(t, gdb, "owner")
	follower := createLocalAccount(t, gdb, "follower")

	list := &models.List{Title: "list", AccountId: owner.Id}
	require.NoError(t, gdb.Create(list).Error)

	listFollowListNotification(&models.ListFollowAccount{ListId: list.Id, AccountId: follower.Id})

	nf := latestNotificationFor(t, gdb, owner.Id, models.NotificationTypeFollow)
	require.NotNil(t, nf, "the list creator should receive a follow notification")
	assert.Equal(t, uint8(models.NotificationTypeFollow), nf.Type)
	assert.Equal(t, list.Id, nf.ListId)
	assert.Equal(t, owner.Id, nf.AccountId)
	assert.Equal(t, follower.Id, nf.FromAccountId)
}

func TestListFollowListNotification_SkipsRemoteCreator(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	remoteOwner := createRemoteAccount(t, gdb, "remoteowner", "fed.host")
	follower := createLocalAccount(t, gdb, "follower")

	list := &models.List{Title: "remote list", AccountId: remoteOwner.Id}
	require.NoError(t, gdb.Create(list).Error)

	before := countNotifications(t, gdb, remoteOwner.Id)

	listFollowListNotification(&models.ListFollowAccount{ListId: list.Id, AccountId: follower.Id})

	assert.Equal(t, before, countNotifications(t, gdb, remoteOwner.Id),
		"a remote list creator must not receive a follow notification")
}

func TestListFollowListNotification_SelfFollowSkips(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	owner := createLocalAccount(t, gdb, "owner")

	list := &models.List{Title: "own list", AccountId: owner.Id}
	require.NoError(t, gdb.Create(list).Error)

	before := countNotifications(t, gdb, owner.Id)

	// The creator following their own list should not produce a notification.
	listFollowListNotification(&models.ListFollowAccount{ListId: list.Id, AccountId: owner.Id})

	assert.Equal(t, before, countNotifications(t, gdb, owner.Id),
		"following one's own list must not create a notification")
}

func TestListFollowListNotification_ListNotFoundSkips(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	follower := createLocalAccount(t, gdb, "follower")

	var total int64
	listFollowListNotification(&models.ListFollowAccount{ListId: 999999, AccountId: follower.Id})
	require.NoError(t, gdb.Model(&models.Notification{}).Where("type = ?", models.NotificationTypeFollow).Count(&total).Error)
	assert.Equal(t, int64(0), total, "if the list cannot be found, no notification is created")
}
