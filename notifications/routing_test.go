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
// status event-handler type routing (after_create / after_update / after_delete)
// The notifications package registers a wildcard "status/*" handler that
// dispatches to the right notification function based on event.Name.
// ---------------------------------------------------------------------------

func TestStatusEvent_NonStatusDataIgnored(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)

	// Wrong data type should not panic and should not dispatch.
	require.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: "not a status"})
	})
}

func TestStatusEvent_AfterCreateRoutesToMention(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	mentioned := createLocalAccount(t, gdb, "mentioned")

	// Build a status with a mention relationship, then trigger after_create.
	// We insert the status + the mention row directly so the handler's DB
	// queries find them.
	status := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, gdb.Create(status).Error)
	require.NoError(t, gdb.Create(&models.StatusToAccount{StatusId: status.Id, AccountId: mentioned.Id}).Error)
	settleEvents()

	require.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_create", Data: status})
	})

	require.Eventually(t, func() bool {
		return len(allNotifications(t, gdb, mentioned.Id, models.NotificationTypeMention)) > 0
	}, time.Second, 10*time.Millisecond, "after_create should route to creatMentionNotification")
}

func TestStatusEvent_AfterDeleteRoutesToRemoveMention(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	mentioned := createLocalAccount(t, gdb, "mentioned")
	status := createStatus(t, gdb, author.Id)

	// Seed a mention notification that after_delete should remove.
	require.NoError(t, gdb.Create(&models.Notification{
		Type:          models.NotificationTypeMention,
		StatusId:      status.Id,
		AccountId:     mentioned.Id,
		FromAccountId: author.Id,
	}).Error)

	require.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "after_delete", Data: status})
	})

	require.Eventually(t, func() bool {
		return len(allNotifications(t, gdb, mentioned.Id, models.NotificationTypeMention)) == 0
	}, time.Second, 10*time.Millisecond, "after_delete should route to removeMentionNotification")
}

func TestStatusEvent_UnknownEventNameNoOp(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := createStatus(t, gdb, author.Id)

	// An unrecognised event name should be a no-op (the switch has no case).
	require.NotPanics(t, func() {
		event.Trigger("status", event.Event{Name: "some_other_event", Data: status})
	})
	settleEvents()

	// Nothing should have been created.
	var total int64
	require.NoError(t, gdb.Model(&models.Notification{}).Count(&total).Error)
	assert.Equal(t, int64(0), total)
}

// ---------------------------------------------------------------------------
// favourite / unfavourite event routing
// ---------------------------------------------------------------------------

func TestFavouriteEvent_RoutesToFavouriteNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	favoriter := createLocalAccount(t, gdb, "favoriter")
	status := createStatus(t, gdb, author.Id)

	fs := &models.FavoriteStatus{
		AccountId: favoriter.Id,
		StatusId:  status.Id,
		Status:    &models.Status{Id: status.Id, AccountId: author.Id, Account: &models.Account{Id: author.Id, Domain: nil}},
	}

	require.NotPanics(t, func() {
		event.Trigger("favourite", event.Event{Name: "favourite", Data: fs})
	})

	require.Eventually(t, func() bool {
		return len(allNotifications(t, gdb, author.Id, models.NotificationTypeFavourite)) > 0
	}, time.Second, 10*time.Millisecond, "favourite event should route to favouriteNotification")
}

func TestUnfavouriteEvent_RoutesToUnfavouriteNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	favoriter := createLocalAccount(t, gdb, "favoriter")
	status := createStatus(t, gdb, author.Id)

	// Seed a favourite notification that unfavourite should remove.
	require.NoError(t, gdb.Create(&models.Notification{
		Type:          models.NotificationTypeFavourite,
		StatusId:      status.Id,
		AccountId:     author.Id,
		FromAccountId: favoriter.Id,
	}).Error)

	fs := &models.FavoriteStatus{
		AccountId: favoriter.Id,
		StatusId:  status.Id,
		Status:    &models.Status{Id: status.Id, AccountId: author.Id},
	}

	require.NotPanics(t, func() {
		event.Trigger("unfavourite", event.Event{Name: "favourite", Data: fs})
	})

	require.Eventually(t, func() bool {
		return len(allNotifications(t, gdb, author.Id, models.NotificationTypeFavourite)) == 0
	}, time.Second, 10*time.Millisecond, "unfavourite event should route to unfavouriteNotification")
}

func TestFavouriteEvent_NonFavoriteStatusDataIgnored(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)

	require.NotPanics(t, func() {
		event.Trigger("favourite", event.Event{Name: "favourite", Data: 12345})
	})
}

// ---------------------------------------------------------------------------
// account event routing (follow / follow-request)
// ---------------------------------------------------------------------------

func TestFollowEvent_RoutesToFollowNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	target := createLocalAccount(t, gdb, "target")
	follower := createLocalAccount(t, gdb, "follower")

	require.NotPanics(t, func() {
		event.Trigger("account", event.Event{
			Name: "follow",
			Data: &models.Follow{AccountId: follower.Id, FollowId: target.Id},
		})
	})

	require.Eventually(t, func() bool {
		return latestNotificationFor(t, gdb, target.Id, models.NotificationTypeFollow) != nil
	}, time.Second, 10*time.Millisecond, "follow event should route to followNotification")
}

func TestFollowRequestEvent_RoutesToFollowRequestNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	target := createLocalAccount(t, gdb, "target")
	requester := createLocalAccount(t, gdb, "requester")

	require.NotPanics(t, func() {
		event.Trigger("account", event.Event{
			Name: "follow-request",
			Data: &models.Follow{AccountId: requester.Id, FollowId: target.Id},
		})
	})

	require.Eventually(t, func() bool {
		return latestNotificationFor(t, gdb, target.Id, models.NotificationTypeFollowRequest) != nil
	}, time.Second, 10*time.Millisecond, "follow-request event should route to followRequestNotification")
}

func TestAccountEvent_NonFollowDataIgnored(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)

	// Both follow and follow-request handlers must tolerate wrong-type data.
	require.NotPanics(t, func() {
		event.Trigger("account", event.Event{Name: "follow", Data: "bad"})
		event.Trigger("account", event.Event{Name: "follow-request", Data: 42})
	})
}

// ---------------------------------------------------------------------------
// list event routing (add-to-list / follow-list)
// ---------------------------------------------------------------------------

func TestAddToListEvent_RoutesToListAddToListNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	owner := createLocalAccount(t, gdb, "owner")
	member := createLocalAccount(t, gdb, "member")
	list := &models.List{Title: "l", AccountId: owner.Id}
	require.NoError(t, gdb.Create(list).Error)

	require.NotPanics(t, func() {
		event.Trigger("list", event.Event{
			Name: "add-to-list",
			Data: &models.ListAccount{ListId: list.Id, AccountId: member.Id},
		})
	})

	require.Eventually(t, func() bool {
		return latestNotificationFor(t, gdb, member.Id, models.NotificationTypeAddList) != nil
	}, time.Second, 10*time.Millisecond, "add-to-list event should route to listAddToListNotification")
}

func TestFollowListEvent_RoutesToListFollowListNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	owner := createLocalAccount(t, gdb, "owner")
	follower := createLocalAccount(t, gdb, "follower")
	list := &models.List{Title: "l", AccountId: owner.Id}
	require.NoError(t, gdb.Create(list).Error)

	require.NotPanics(t, func() {
		event.Trigger("list", event.Event{
			Name: "follow-list",
			Data: &models.ListFollowAccount{ListId: list.Id, AccountId: follower.Id},
		})
	})

	require.Eventually(t, func() bool {
		return latestNotificationFor(t, gdb, owner.Id, models.NotificationTypeFollow) != nil
	}, time.Second, 10*time.Millisecond, "follow-list event should route to listFollowListNotification")
}

func TestListEvent_NonListDataIgnored(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)

	require.NotPanics(t, func() {
		event.Trigger("list", event.Event{Name: "add-to-list", Data: "bad"})
		event.Trigger("list", event.Event{Name: "follow-list", Data: 42})
	})
}

// ---------------------------------------------------------------------------
// poll event routing (finish)
// ---------------------------------------------------------------------------

func TestPollFinishEvent_RoutesToFinishPollNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	voter := createLocalAccount(t, gdb, "voter")
	status := createStatus(t, gdb, author.Id)

	past := time.Now().Add(-1 * time.Hour)
	poll := &models.Poll{StatusId: status.Id, AccountId: author.Id, ExpiresAt: &past}
	require.NoError(t, gdb.Create(poll).Error)
	require.NoError(t, gdb.Create(&models.PollOptionVote{PollId: poll.Id, AccountId: voter.Id}).Error)

	require.NotPanics(t, func() {
		event.Trigger("poll", event.Event{Name: "finish", Data: poll})
	})

	require.Eventually(t, func() bool {
		return len(allNotifications(t, gdb, voter.Id, models.NotificationTypePoll)) > 0
	}, time.Second, 10*time.Millisecond, "finish event should route to finishPollNotification")
}
