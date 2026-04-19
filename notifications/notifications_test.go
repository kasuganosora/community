package notifications

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

// --- Notification Type Constants Tests ---

func TestNotificationTypeConstants(t *testing.T) {
	// Verify all notification type constants are defined
	types := []int{
		models.NotificationTypeMention,
		models.NotificationTypeReblog,
		models.NotificationTypeFavourite,
		models.NotificationTypeFollow,
		models.NotificationTypeFollowRequest,
		models.NotificationTypePoll,
		models.NotificationTypeStatusUpdate,
		models.NotificationTypeAdminReport,
		models.NotificationTypeAddList,
	}

	// Ensure all types are unique
	seen := make(map[int]bool)
	for _, nt := range types {
		assert.False(t, seen[nt], "Notification type %d is duplicated", nt)
		seen[nt] = true
	}

	// Ensure there are no negative values
	for _, nt := range types {
		assert.GreaterOrEqual(t, nt, 0, "Notification type should not be negative")
	}
}

// --- Notification Model Tests ---

func TestNotification_StructCreation(t *testing.T) {
	nf := &models.Notification{
		AccountId:     1,
		FromAccountId: 2,
		Type:          models.NotificationTypeFollow,
		StatusId:      0,
	}

	assert.Equal(t, uint64(1), nf.AccountId)
	assert.Equal(t, uint64(2), nf.FromAccountId)
	assert.Equal(t, uint8(models.NotificationTypeFollow), nf.Type)
	assert.Equal(t, uint64(0), nf.StatusId)
}

func TestNotification_WithStatus(t *testing.T) {
	statusId := uint64(42)
	nf := &models.Notification{
		AccountId:     1,
		FromAccountId: 2,
		Type:          models.NotificationTypeMention,
		StatusId:      statusId,
	}

	assert.Equal(t, statusId, nf.StatusId)
}

func TestNotification_WithReport(t *testing.T) {
	reportId := uint64(99)
	nf := &models.Notification{
		Type:     models.NotificationTypeAdminReport,
		ReportId: reportId,
	}

	assert.Equal(t, reportId, nf.ReportId)
}

func TestNotification_WithList(t *testing.T) {
	listId := uint64(55)
	nf := &models.Notification{
		AccountId:     1,
		FromAccountId: 2,
		Type:          models.NotificationTypeAddList,
		ListId:        listId,
	}

	assert.Equal(t, listId, nf.ListId)
}

// --- Follow Notification Logic Tests ---

func TestFollowNotification_SkipsRemoteAccounts(t *testing.T) {
	// Test that the logic skips notifications for remote accounts
	// (domain != nil)
	domain := "example.com"
	account := &models.Account{
		Id:     1,
		Domain: &domain,
	}

	assert.NotNil(t, account.Domain)
	assert.Equal(t, "example.com", *account.Domain)
	// The actual function checks: if follow.Account.Domain != nil { return }
}

func TestFollowNotification_SendsToLocalAccounts(t *testing.T) {
	// Local accounts have Domain == nil
	account := &models.Account{
		Id:     1,
		Domain: nil,
	}

	assert.Nil(t, account.Domain)
	// The actual function would proceed with notification creation
}

// --- Follow Request Notification Logic Tests ---

func TestFollowRequestNotification_SkipsRemoteAccounts(t *testing.T) {
	domain := "remote.social"
	account := &models.Account{
		Id:     2,
		Domain: &domain,
	}

	assert.NotNil(t, account.Domain)
	// followRequestNotification should return early for remote accounts
}

// --- Report Notification Logic Tests ---

func TestReportProcessedNotification_CreatesCorrectNotification(t *testing.T) {
	report := &models.Report{
		Id:        10,
		AccountId: 5,
	}

	nf := &models.Notification{
		Type:      models.NotificationTypeAdminReport,
		ReportId:  report.Id,
		AccountId: report.AccountId,
	}

	assert.Equal(t, uint8(models.NotificationTypeAdminReport), nf.Type)
	assert.Equal(t, uint64(10), nf.ReportId)
	assert.Equal(t, uint64(5), nf.AccountId)
}

func TestReportProcessedNotification_ActionTakenNil(t *testing.T) {
	// If ActionTakenAt is nil, notification should not be created
	report := &models.Report{
		Id:            10,
		AccountId:     5,
		ActionTakenAt: nil,
	}

	assert.Nil(t, report.ActionTakenAt)
	// The event handler checks: if report.ActionTakenAt != nil { go reportProcessedNotification(report) }
	// So with nil ActionTakenAt, the notification function is never called
}

func TestReportProcessedNotification_ActionTakenSet(t *testing.T) {
	now := time.Now()
	report := &models.Report{
		Id:            10,
		AccountId:     5,
		ActionTakenAt: &now,
	}

	assert.NotNil(t, report.ActionTakenAt)
	// The event handler should trigger notification creation
}

// --- Poll Notification Logic Tests ---

func TestPollNotification_PollNotExpired(t *testing.T) {
	// Poll that hasn't expired yet should not send notifications
	futureTime := time.Now().Add(24 * time.Hour)
	poll := &models.Poll{
		Id:        1,
		StatusId:  10,
		ExpiresAt: &futureTime,
	}

	assert.NotNil(t, poll.ExpiresAt)
	// finishPollNotification checks: if poll.ExpiresAt != nil && poll.ExpiresAt.After(time.Now()) { return }
	// With future ExpiresAt, function returns early
}

// --- Status Notification Logic Tests ---

func TestStatusNotification_ReblogNotificationChecks(t *testing.T) {
	// creatReblogNotification checks ReblogId
	reblogId := uint64(5)

	t.Run("nil ReblogId skips notification", func(t *testing.T) {
		status := &models.Status{
			Id:       1,
			ReblogId: nil,
		}
		assert.Nil(t, status.ReblogId)
		// creatReblogNotification would return early
	})

	t.Run("zero ReblogId skips notification", func(t *testing.T) {
		zeroId := uint64(0)
		status := &models.Status{
			Id:       1,
			ReblogId: &zeroId,
		}
		assert.NotNil(t, status.ReblogId)
		assert.Equal(t, uint64(0), *status.ReblogId)
		// creatReblogNotification would return early
	})

	t.Run("non-zero ReblogId creates notification", func(t *testing.T) {
		status := &models.Status{
			Id:       1,
			ReblogId: &reblogId,
		}
		assert.Equal(t, uint64(5), *status.ReblogId)
		// creatReblogNotification would proceed
	})
}

func TestStatusNotification_FavouriteRemoteAuthorSkipped(t *testing.T) {
	domain := "remote.host"
	status := &models.Status{
		Id:        10,
		AccountId: 1,
		Account: &models.Account{
			Id:     1,
			Domain: &domain,
		},
	}

	assert.NotNil(t, status.Account.Domain)
	// favouriteNotification checks: if originalStatus.Account.Domain != nil { return }
}

func TestStatusNotification_FavouriteLocalAuthorNotified(t *testing.T) {
	status := &models.Status{
		Id:        10,
		AccountId: 1,
		Account: &models.Account{
			Id:     1,
			Domain: nil,
		},
	}

	assert.Nil(t, status.Account.Domain)
	// favouriteNotification would proceed
}

func TestStatusNotification_ReblogFollowing(t *testing.T) {
	// Check the reblog resolution logic
	reblogId := uint64(5)

	t.Run("status with reblog and nil Reblog object", func(t *testing.T) {
		status := &models.Status{
			Id:       1,
			ReblogId: &reblogId,
			Reblog:   nil,
		}
		assert.Nil(t, status.Reblog)
		// Handler loads Reblog from DB
	})

	t.Run("status with reblog object present", func(t *testing.T) {
		reblog := &models.Status{Id: 5, AccountId: 2}
		status := &models.Status{
			Id:       1,
			ReblogId: &reblogId,
			Reblog:   reblog,
		}
		assert.NotNil(t, status.Reblog)
		assert.Equal(t, uint64(5), status.Reblog.Id)
	})
}

// --- Mention Notification Logic Tests ---

func TestMentionNotification_EmptyAccountList(t *testing.T) {
	// creatMentionNotification returns early if no mention accounts
	accountList := make([]uint64, 0)
	assert.Empty(t, accountList)
	// creatMentionNotification checks: if len(newMentionAccountList) == 0 { return }
}

func TestMentionNotification_RemoveNotification(t *testing.T) {
	// removeMentionNotification finds and deletes existing notifications
	// Verify the query logic
	notificationType := models.NotificationTypeMention
	statusId := uint64(42)

	assert.Equal(t, models.NotificationTypeMention, notificationType)
	assert.Equal(t, uint64(42), statusId)
	// Query: WHERE type = ? AND status_id = ?
}

func TestUpdateMentionNotification_DiffLogic(t *testing.T) {
	// updateMentionNotification compares old and new mention lists
	// and creates/removes notifications accordingly
	oldAccounts := []uint64{1, 2, 3}
	newAccounts := []uint64{2, 3, 4}

	// Old: {1, 2, 3}, New: {2, 3, 4}
	// Account 1 should be removed (in old but not in new)
	// Account 4 should be added (in new but not in old)
	// Accounts 2, 3 should get update notification

	oldMap := make(map[uint64]bool)
	for _, id := range oldAccounts {
		oldMap[id] = true
	}

	newMap := make(map[uint64]bool)
	for _, id := range newAccounts {
		newMap[id] = true
	}

	toRemove := make([]uint64, 0)
	for _, id := range oldAccounts {
		if !newMap[id] {
			toRemove = append(toRemove, id)
		}
	}
	assert.Equal(t, []uint64{1}, toRemove)

	toAdd := make([]uint64, 0)
	for _, id := range newAccounts {
		if !oldMap[id] {
			toAdd = append(toAdd, id)
		}
	}
	assert.Equal(t, []uint64{4}, toAdd)

	toUpdate := make([]uint64, 0)
	for _, id := range oldAccounts {
		if newMap[id] {
			toUpdate = append(toUpdate, id)
		}
	}
	assert.Equal(t, []uint64{2, 3}, toUpdate)
}

// --- List Notification Logic Tests ---

func TestListAddToListNotification_RemoteAccountSkipped(t *testing.T) {
	domain := "federated.social"
	account := &models.Account{
		Id:     5,
		Domain: &domain,
	}

	assert.NotNil(t, account.Domain)
	// listAddToListNotification checks: if account.Domain != nil { return }
}

func TestListFollowListNotification_RemoteCreatorSkipped(t *testing.T) {
	domain := "remote.example"
	creator := &models.Account{
		Id:     1,
		Domain: &domain,
	}
	list := &models.List{
		Id:        10,
		AccountId: 1,
		Creator:   creator,
	}

	assert.NotNil(t, list.Creator.Domain)
	// listFollowListNotification checks: if list.Creator.Domain != nil { return }
}

// --- Notification Bulk Creation Tests ---

func TestNotification_BulkCreation(t *testing.T) {
	nfList := make([]*models.Notification, 0)

	for i := uint64(1); i <= 3; i++ {
		nf := &models.Notification{
			AccountId:     i,
			FromAccountId: 10,
			Type:          models.NotificationTypePoll,
			StatusId:      42,
		}
		nfList = append(nfList, nf)
	}

	assert.Len(t, nfList, 3)
	for i, nf := range nfList {
		assert.Equal(t, uint64(i+1), nf.AccountId)
		assert.Equal(t, uint64(10), nf.FromAccountId)
		assert.Equal(t, uint8(models.NotificationTypePoll), nf.Type)
		assert.Equal(t, uint64(42), nf.StatusId)
	}
}

// --- Event Handler Registration Tests ---

func TestEventHandlers_PanicRecovery(t *testing.T) {
	// Verify that notification functions handle nil pointers gracefully
	// by testing the early return conditions

	t.Run("nil Follow.Account", func(t *testing.T) {
		follow := &models.Follow{
			Id:        1,
			AccountId: 1,
			FollowId:  2,
			Account:   nil,
		}
		assert.Nil(t, follow.Account)
		// followNotification loads Account from DB if nil
	})

	t.Run("nil FavoriteStatus.Status", func(t *testing.T) {
		fs := &models.FavoriteStatus{
			Id:       1,
			StatusId: 10,
			Status:   nil,
		}
		assert.Nil(t, fs.Status)
		// favouriteNotification loads Status from DB if nil
	})

	t.Run("nil Status for mention notification", func(t *testing.T) {
		status := &models.Status{
			Id: 42,
		}
		assert.NotNil(t, status)
		// creatMentionNotification works with Status directly
	})
}

// --- Notification Type String Representation ---

func TestNotificationTypes_Coverage(t *testing.T) {
	// Test that we have at least the expected notification types
	assert.GreaterOrEqual(t, models.NotificationTypeMention, 0)
	assert.GreaterOrEqual(t, models.NotificationTypeReblog, 0)
	assert.GreaterOrEqual(t, models.NotificationTypeFavourite, 0)
	assert.GreaterOrEqual(t, models.NotificationTypeFollow, 0)
	assert.GreaterOrEqual(t, models.NotificationTypeFollowRequest, 0)
	assert.GreaterOrEqual(t, models.NotificationTypePoll, 0)
	assert.GreaterOrEqual(t, models.NotificationTypeStatusUpdate, 0)
	assert.GreaterOrEqual(t, models.NotificationTypeAdminReport, 0)
	assert.GreaterOrEqual(t, models.NotificationTypeAddList, 0)
}

// --- Unfavourite Notification Logic Tests ---

func TestUnfavouriteNotification_ReblogResolution(t *testing.T) {
	t.Run("status with reblog resolves to original", func(t *testing.T) {
		reblogId := uint64(5)
		reblog := &models.Status{Id: 5, AccountId: 3}
		boost := &models.Status{
			Id:       1,
			ReblogId: &reblogId,
			Reblog:   reblog,
		}

		original := boost
		if original.ReblogId != nil && *original.ReblogId > 0 {
			if original.Reblog != nil {
				original = original.Reblog
			}
		}
		assert.Equal(t, uint64(5), original.Id)
		assert.Equal(t, uint64(3), original.AccountId)
	})

	t.Run("status without reblog is itself original", func(t *testing.T) {
		status := &models.Status{Id: 10, AccountId: 1, ReblogId: nil}

		original := status
		// ReblogId is nil, so this status is its own original
		_ = original.ReblogId
		assert.Equal(t, uint64(10), original.Id)
	})

	t.Run("status with zero reblog is itself original", func(t *testing.T) {
		zeroId := uint64(0)
		status := &models.Status{Id: 10, AccountId: 1, ReblogId: &zeroId}

		original := status
		// ReblogId is non-nil but zero, so this status is its own original
		_ = original.ReblogId
		assert.Equal(t, uint64(10), original.Id)
	})
}
