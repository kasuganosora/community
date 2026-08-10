package notifications

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// creatMentionNotification
// ---------------------------------------------------------------------------

func TestCreatMentionNotification_CreatesForLocalMentions(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	mentioned1 := createLocalAccount(t, gdb, "m1")
	mentioned2 := createLocalAccount(t, gdb, "m2")
	status := createStatus(t, gdb, author.Id)

	// Two mention relationships.
	require.NoError(t, gdb.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: mentioned1.Id}).Error)
	require.NoError(t, gdb.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: mentioned2.Id}).Error)

	creatMentionNotification(status)

	for _, mentioned := range []*models.Account{mentioned1, mentioned2} {
		ns := allNotifications(t, gdb, mentioned.Id, models.NotificationTypeMention)
		require.Len(t, ns, 1, "each mentioned local user should receive one notification")
		assert.Equal(t, status.Id, ns[0].StatusId)
		assert.Equal(t, author.Id, ns[0].FromAccountId)
	}
}

func TestCreatMentionNotification_SkipsAuthorSelfMention(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := createStatus(t, gdb, author.Id)

	// Only mentions the author themselves.
	require.NoError(t, gdb.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: author.Id}).Error)

	creatMentionNotification(status)

	ns := allNotifications(t, gdb, author.Id, models.NotificationTypeMention)
	assert.Empty(t, ns, "self-mention must not produce a notification")
}

func TestCreatMentionNotification_NoMentionsSendsNothing(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := createStatus(t, gdb, author.Id)

	// Empty-batch guard: no mention rows -> no Create call.
	assert.NotPanics(t, func() {
		creatMentionNotification(status)
	})

	var total int64
	require.NoError(t, gdb.Model(&models.Notification{}).Where("type = ?", models.NotificationTypeMention).Count(&total).Error)
	assert.Equal(t, int64(0), total)
}

func TestCreatMentionNotification_SkipsRemoteMentions(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	remote := createRemoteAccount(t, gdb, "remote", "host.example")
	status := createStatus(t, gdb, author.Id)

	require.NoError(t, gdb.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: remote.Id}).Error)

	creatMentionNotification(status)

	ns := allNotifications(t, gdb, remote.Id, models.NotificationTypeMention)
	assert.Empty(t, ns, "remote mentioned users must not receive notifications")
}

// ---------------------------------------------------------------------------
// updateMentionNotification
// ---------------------------------------------------------------------------

func TestUpdateMentionNotification_AddsAndRemoves(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	old := createLocalAccount(t, gdb, "old") // will be removed in the update
	keep := createLocalAccount(t, gdb, "keep")
	added := createLocalAccount(t, gdb, "added")
	status := createStatus(t, gdb, author.Id)

	// Pre-existing mention notifications: "old" and "keep".
	require.NoError(t, gdb.Create(&models.Notification{
		Type:          models.NotificationTypeMention,
		StatusId:      status.Id,
		AccountId:     old.Id,
		FromAccountId: author.Id,
	}).Error)
	require.NoError(t, gdb.Create(&models.Notification{
		Type:          models.NotificationTypeMention,
		StatusId:      status.Id,
		AccountId:     keep.Id,
		FromAccountId: author.Id,
	}).Error)

	// New mention set: "keep" (retained) and "added" (new). "old" no longer mentioned.
	require.NoError(t, gdb.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: keep.Id}).Error)
	require.NoError(t, gdb.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: added.Id}).Error)

	updateMentionNotification(status)

	// "old" mention notification should be deleted.
	assert.Empty(t, allNotifications(t, gdb, old.Id, models.NotificationTypeMention),
		"removed mention should have its notification deleted")

	// "added" should get a new mention notification.
	addedNs := allNotifications(t, gdb, added.Id, models.NotificationTypeMention)
	require.Len(t, addedNs, 1, "newly mentioned user should get a mention notification")
	assert.Equal(t, status.Id, addedNs[0].StatusId)

	// "keep" should get a status_update notification (it was already mentioned).
	updateNs := allNotifications(t, gdb, keep.Id, models.NotificationTypeStatusUpdate)
	require.Len(t, updateNs, 1, "retained mention should get a status_update notification")
	assert.Equal(t, status.Id, updateNs[0].StatusId)
}

func TestUpdateMentionNotification_NoChangesKeepsExisting(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	m1 := createLocalAccount(t, gdb, "m1")
	status := createStatus(t, gdb, author.Id)

	require.NoError(t, gdb.Create(&models.Notification{
		Type:          models.NotificationTypeMention,
		StatusId:      status.Id,
		AccountId:     m1.Id,
		FromAccountId: author.Id,
	}).Error)
	require.NoError(t, gdb.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: m1.Id}).Error)

	updateMentionNotification(status)

	// Mention notification remains, and a status_update is created for the retained mention.
	assert.Len(t, allNotifications(t, gdb, m1.Id, models.NotificationTypeMention), 1)
	assert.Len(t, allNotifications(t, gdb, m1.Id, models.NotificationTypeStatusUpdate), 1)
}

// ---------------------------------------------------------------------------
// removeMentionNotification
// ---------------------------------------------------------------------------

func TestRemoveMentionNotification_DeletesExisting(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	m1 := createLocalAccount(t, gdb, "m1")
	status := createStatus(t, gdb, author.Id)

	require.NoError(t, gdb.Create(&models.Notification{
		Type:          models.NotificationTypeMention,
		StatusId:      status.Id,
		AccountId:     m1.Id,
		FromAccountId: author.Id,
	}).Error)

	removeMentionNotification(status)

	assert.Empty(t, allNotifications(t, gdb, m1.Id, models.NotificationTypeMention),
		"removeMentionNotification should delete existing mention notifications")
}

func TestRemoveMentionNotification_NothingToDelete(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := createStatus(t, gdb, author.Id)

	// No existing mention notifications -> early return, no panic.
	assert.NotPanics(t, func() {
		removeMentionNotification(status)
	})
}
