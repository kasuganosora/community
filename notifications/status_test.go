package notifications

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// creatReblogNotification
// ---------------------------------------------------------------------------

func TestCreatReblogNotification_NilReblogIdSkips(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	booster := createLocalAccount(t, gdb, "booster")

	status := &models.Status{AccountId: booster.Id, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, gdb.Create(status).Error)
	settleEvents()

	before := countNotifications(t, gdb, booster.Id)

	// No ReblogId -> early return.
	creatReblogNotification(status)

	assert.Equal(t, before, countNotifications(t, gdb, booster.Id),
		"a status without ReblogId must not create a reblog notification")
}

func TestCreatReblogNotification_ZeroReblogIdSkips(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	booster := createLocalAccount(t, gdb, "booster")
	zero := uint64(0)
	status := &models.Status{AccountId: booster.Id, Visibility: models.StatusVisibilityPublic, ReblogId: &zero}
	require.NoError(t, gdb.Create(status).Error)
	settleEvents()

	// ReblogId == 0 -> early return (no notification, no panic).
	assert.NotPanics(t, func() {
		creatReblogNotification(status)
	})
}

func TestCreatReblogNotification_CreatesForLocalAuthor(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	originalAuthor := createLocalAccount(t, gdb, "original")
	booster := createLocalAccount(t, gdb, "booster")

	// Original status by originalAuthor.
	original := createStatus(t, gdb, originalAuthor.Id)

	// The boost references the original; we set Reblog inline so getReblogStatus
	// uses it directly (no extra DB lookup), and Account inline so the booster
	// account is already loaded.
	reblogId := original.Id
	boost := &models.Status{
		AccountId:  booster.Id,
		Visibility: models.StatusVisibilityPublic,
		ReblogId:   &reblogId,
		Reblog:     original,
		Account:    &models.Account{Id: booster.Id, Domain: nil},
	}

	creatReblogNotification(boost)

	ns := allNotifications(t, gdb, originalAuthor.Id, models.NotificationTypeReblog)
	require.Len(t, ns, 1, "the original author should receive one reblog notification")
	assert.Equal(t, original.Id, ns[0].StatusId)
	assert.Equal(t, booster.Id, ns[0].FromAccountId)
	assert.Equal(t, originalAuthor.Id, ns[0].AccountId)
}

func TestCreatReblogNotification_SkipsRemoteAuthor(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	remoteAuthor := createRemoteAccount(t, gdb, "remoteauthor", "fed.host")
	booster := createLocalAccount(t, gdb, "booster")

	original := createStatus(t, gdb, remoteAuthor.Id)
	remoteAuthor2 := &models.Account{Id: remoteAuthor.Id, Domain: strPtr("fed.host")}

	reblogId := original.Id
	boost := &models.Status{
		AccountId:  booster.Id,
		Visibility: models.StatusVisibilityPublic,
		ReblogId:   &reblogId,
		Reblog:     original,
		Account:    &models.Account{Id: booster.Id, Domain: nil},
	}
	// getStatusAccount returns the booster's account (local). To exercise the
	// "remote author" branch we make the *reblog* (original) belong to a remote
	// author. getReblogStatus returns original; then we check originalAuthor.
	// The remote-skip is based on the booster's account (status.Account), so we
	// instead exercise the booster being remote:
	boost.Account = remoteAuthor2

	before := countNotifications(t, gdb, remoteAuthor.Id)

	creatReblogNotification(boost)

	assert.Equal(t, before, countNotifications(t, gdb, remoteAuthor.Id),
		"a reblog by a remote account must not notify the (local) original author")
}

func TestCreatReblogNotification_SelfReblogSkips(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	original := createStatus(t, gdb, author.Id)

	reblogId := original.Id
	boost := &models.Status{
		AccountId:  author.Id, // same as original author
		Visibility: models.StatusVisibilityPublic,
		ReblogId:   &reblogId,
		Reblog:     original,
		Account:    &models.Account{Id: author.Id, Domain: nil},
	}

	before := countNotifications(t, gdb, author.Id)

	creatReblogNotification(boost)

	assert.Equal(t, before, countNotifications(t, gdb, author.Id),
		"self-reblog must not create a notification")
}

// ---------------------------------------------------------------------------
// updateReblogNotification / removeReblogNotification
// ---------------------------------------------------------------------------

func TestUpdateReblogNotification_NilReblogIdSkips(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, gdb.Create(status).Error)
	settleEvents()

	assert.NotPanics(t, func() {
		updateReblogNotification(status)
	})
}

func TestUpdateReblogNotification_NotifiesRebloggers(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	originalAuthor := createLocalAccount(t, gdb, "original")
	reblogger := createLocalAccount(t, gdb, "reblogger")
	original := createStatus(t, gdb, originalAuthor.Id)

	// A reblog status whose account_id is the reblogger. updateReblogNotification
	// queries statuses WHERE reblog_id = original.Id to find who reblogged.
	reblogId := original.Id
	reblogStatus := &models.Status{
		AccountId:  reblogger.Id,
		Visibility: models.StatusVisibilityPublic,
		ReblogId:   &reblogId,
	}
	require.NoError(t, gdb.Create(reblogStatus).Error)
	settleEvents()

	// The "status" being updated is a fresh boost object referencing the original.
	boost := &models.Status{
		AccountId: originalAuthor.Id,
		ReblogId:  &reblogId,
		Reblog:    original,
	}

	updateReblogNotification(boost)

	// The reblogger (a local account) should get a status_update notification
	// for the original status.
	ns := allNotifications(t, gdb, reblogger.Id, models.NotificationTypeStatusUpdate)
	require.Len(t, ns, 1, "reblogger should receive one status_update notification")
	assert.Equal(t, original.Id, ns[0].StatusId)
	assert.Equal(t, originalAuthor.Id, ns[0].FromAccountId)
}

func TestRemoveReblogNotification_NilReblogIdSkips(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic}
	require.NoError(t, gdb.Create(status).Error)
	settleEvents()

	assert.NotPanics(t, func() {
		removeReblogNotification(status)
	})
}

func TestRemoveReblogNotification_DeletesExisting(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	originalAuthor := createLocalAccount(t, gdb, "original")
	booster := createLocalAccount(t, gdb, "booster")
	original := createStatus(t, gdb, originalAuthor.Id)

	// Seed a reblog notification.
	require.NoError(t, gdb.Create(&models.Notification{
		Type:          models.NotificationTypeReblog,
		StatusId:      original.Id,
		AccountId:     originalAuthor.Id,
		FromAccountId: booster.Id,
	}).Error)

	reblogId := original.Id
	boost := &models.Status{
		AccountId: booster.Id,
		ReblogId:  &reblogId,
		Reblog:    original,
	}

	removeReblogNotification(boost)

	assert.Empty(t, allNotifications(t, gdb, originalAuthor.Id, models.NotificationTypeReblog),
		"removeReblogNotification should delete the existing reblog notification")
}

// ---------------------------------------------------------------------------
// favouriteNotification / unfavouriteNotification / getOriginalStatus
// ---------------------------------------------------------------------------

func TestFavouriteNotification_CreatesForLocalAuthor(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	favoriter := createLocalAccount(t, gdb, "favoriter")
	status := createStatus(t, gdb, author.Id)

	fs := &models.FavoriteStatus{
		AccountId: favoriter.Id,
		StatusId:  status.Id,
		Status: &models.Status{
			Id:        status.Id,
			AccountId: author.Id,
			Account:   &models.Account{Id: author.Id, Domain: nil},
		},
	}

	favouriteNotification(fs)

	ns := allNotifications(t, gdb, author.Id, models.NotificationTypeFavourite)
	require.Len(t, ns, 1)
	assert.Equal(t, status.Id, ns[0].StatusId)
	assert.Equal(t, favoriter.Id, ns[0].FromAccountId)
	assert.Equal(t, author.Id, ns[0].AccountId)
}

func TestFavouriteNotification_SkipsAuthorSelfFavourite(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := createStatus(t, gdb, author.Id)

	fs := &models.FavoriteStatus{
		AccountId: author.Id, // same as status author
		StatusId:  status.Id,
		Status: &models.Status{
			Id:        status.Id,
			AccountId: author.Id,
			Account:   &models.Account{Id: author.Id, Domain: nil},
		},
	}

	before := countNotifications(t, gdb, author.Id)

	favouriteNotification(fs)

	assert.Equal(t, before, countNotifications(t, gdb, author.Id),
		"favouriting one's own status must not create a notification")
}

func TestFavouriteNotification_SkipsRemoteAuthor(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	remoteAuthor := createRemoteAccount(t, gdb, "remoteauthor", "fed.host")
	favoriter := createLocalAccount(t, gdb, "favoriter")
	status := createStatus(t, gdb, remoteAuthor.Id)

	fs := &models.FavoriteStatus{
		AccountId: favoriter.Id,
		StatusId:  status.Id,
		Status: &models.Status{
			Id:        status.Id,
			AccountId: remoteAuthor.Id,
			Account:   &models.Account{Id: remoteAuthor.Id, Domain: strPtr("fed.host")},
		},
	}

	before := countNotifications(t, gdb, remoteAuthor.Id)

	favouriteNotification(fs)

	assert.Equal(t, before, countNotifications(t, gdb, remoteAuthor.Id),
		"favouriting a remote author's status must not notify them")
}

func TestFavouriteNotification_ResolvesReblogToOriginal(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	originalAuthor := createLocalAccount(t, gdb, "original")
	booster := createLocalAccount(t, gdb, "booster")
	favoriter := createLocalAccount(t, gdb, "favoriter")

	original := createStatus(t, gdb, originalAuthor.Id)
	boost := createStatus(t, gdb, booster.Id)

	// fs points at the boost, but the favourite should target the original.
	reblogId := original.Id
	fs := &models.FavoriteStatus{
		AccountId: favoriter.Id,
		StatusId:  boost.Id,
		Status: &models.Status{
			Id:        boost.Id,
			AccountId: booster.Id,
			ReblogId:  &reblogId,
			Reblog:    &models.Status{Id: original.Id, AccountId: originalAuthor.Id, Account: &models.Account{Id: originalAuthor.Id, Domain: nil}},
		},
	}

	favouriteNotification(fs)

	// Notification should be for the ORIGINAL status, addressed to originalAuthor.
	ns := allNotifications(t, gdb, originalAuthor.Id, models.NotificationTypeFavourite)
	require.Len(t, ns, 1)
	assert.Equal(t, original.Id, ns[0].StatusId)
	assert.Equal(t, favoriter.Id, ns[0].FromAccountId)
}

func TestUnfavouriteNotification_RemovesExisting(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	favoriter := createLocalAccount(t, gdb, "favoriter")
	status := createStatus(t, gdb, author.Id)

	// Seed a favourite notification.
	require.NoError(t, gdb.Create(&models.Notification{
		Type:          models.NotificationTypeFavourite,
		StatusId:      status.Id,
		AccountId:     author.Id,
		FromAccountId: favoriter.Id,
	}).Error)

	fs := &models.FavoriteStatus{
		AccountId: favoriter.Id,
		StatusId:  status.Id,
		Status: &models.Status{
			Id:        status.Id,
			AccountId: author.Id,
		},
	}

	unfavouriteNotification(fs)

	assert.Empty(t, allNotifications(t, gdb, author.Id, models.NotificationTypeFavourite),
		"unfavouriteNotification should remove the existing favourite notification")
}

func TestUnfavouriteNotification_NotFoundNoOp(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	favoriter := createLocalAccount(t, gdb, "favoriter")
	status := createStatus(t, gdb, author.Id)

	fs := &models.FavoriteStatus{
		AccountId: favoriter.Id,
		StatusId:  status.Id,
		Status: &models.Status{
			Id:        status.Id,
			AccountId: author.Id,
		},
	}

	// No existing notification -> First returns ErrRecordNotFound -> no panic.
	assert.NotPanics(t, func() {
		unfavouriteNotification(fs)
	})
}

// ---------------------------------------------------------------------------
// getOriginalStatus / getReblogStatus / getStatusAccount
// ---------------------------------------------------------------------------

func TestGetOriginalStatus_LoadsFromDBWhenStatusNil(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")
	status := createStatus(t, gdb, author.Id)

	// fs.Status is nil -> function queries the DB by StatusId.
	got := getOriginalStatus(&models.FavoriteStatus{AccountId: author.Id, StatusId: status.Id})
	require.NotNil(t, got)
	assert.Equal(t, status.Id, got.Id)
}

func TestGetOriginalStatus_StatusNotFoundReturnsNil(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)

	got := getOriginalStatus(&models.FavoriteStatus{AccountId: 1, StatusId: 999999})
	assert.Nil(t, got, "getOriginalStatus must return nil for a missing status")
}

func TestGetOriginalStatus_ReblogNilWhenIdSetReturnsNil(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)
	reblogId := uint64(5)
	// Build an in-memory status only (not persisted) so we don't share a
	// pointer with event-handler goroutines that read status.ReblogId.
	boost := &models.Status{
		Id:       999,
		ReblogId: &reblogId,
		Reblog:   nil, // ReblogId set but Reblog object missing -> nil
	}

	got := getOriginalStatus(&models.FavoriteStatus{
		AccountId: 1,
		StatusId:  boost.Id,
		Status:    boost,
	})
	assert.Nil(t, got, "ReblogId set with nil Reblog should yield nil original status")
}

func TestGetReblogStatus_LoadsFromDBWhenReblogNil(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	originalAuthor := createLocalAccount(t, gdb, "original")
	original := createStatus(t, gdb, originalAuthor.Id)

	reblogId := original.Id
	boost := &models.Status{Id: 999, ReblogId: &reblogId, Reblog: nil}

	got, err := getReblogStatus(boost)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, original.Id, got.Id)
}

func TestGetReblogStatus_NotFoundReturnsError(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)

	reblogId := uint64(999999)
	boost := &models.Status{Id: 1, ReblogId: &reblogId, Reblog: nil}

	got, err := getReblogStatus(boost)
	assert.Error(t, err)
	assert.Nil(t, got)
}

func TestGetStatusAccount_LoadsFromDBWhenAccountNil(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	author := createLocalAccount(t, gdb, "author")

	got, err := getStatusAccount(&models.Status{Id: 1, AccountId: author.Id, Account: nil})
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, author.Id, got.Id)
	assert.Nil(t, got.Domain, "local account has nil Domain")
}

func TestGetStatusAccount_NotFoundReturnsError(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)

	got, err := getStatusAccount(&models.Status{Id: 1, AccountId: 999999, Account: nil})
	assert.Error(t, err)
	assert.Nil(t, got)
}

// strPtr returns a pointer to s (helper for building remote-domain accounts inline).
func strPtr(s string) *string { return &s }
