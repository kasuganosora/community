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
// reportProcessedNotification
// ---------------------------------------------------------------------------

func TestReportProcessedNotification_CreatesNotification(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	reporter := createLocalAccount(t, gdb, "reporter")

	now := time.Now()
	report := &models.Report{
		AccountId:     reporter.Id,
		Comment:       "spam report",
		ActionTakenAt: &now,
	}
	require.NoError(t, gdb.Create(report).Error, "failed to create report")

	reportProcessedNotification(report)

	nf := latestNotificationFor(t, gdb, reporter.Id, models.NotificationTypeAdminReport)
	require.NotNil(t, nf, "an admin_report notification should be created")
	assert.Equal(t, uint8(models.NotificationTypeAdminReport), nf.Type)
	assert.Equal(t, report.Id, nf.ReportId)
	assert.Equal(t, reporter.Id, nf.AccountId)
	assert.Equal(t, uint64(0), nf.StatusId, "admin_report notification has no status")
}

func TestReportProcessedNotification_UsesAccountIdFromReport(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	alice := createLocalAccount(t, gdb, "alice")

	report := &models.Report{AccountId: alice.Id}
	reportProcessedNotification(report)

	// Exactly one notification, addressed to the reporter (alice).
	ns := allNotifications(t, gdb, alice.Id, models.NotificationTypeAdminReport)
	require.Len(t, ns, 1)
	assert.Equal(t, alice.Id, ns[0].AccountId)
}

// ---------------------------------------------------------------------------
// Event-handler routing (report "after_update") via the registered handler
// ---------------------------------------------------------------------------

func TestReportEvent_NoActionTakenDoesNotNotify(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	alice := createLocalAccount(t, gdb, "alice")

	before := countNotifications(t, gdb, alice.Id)

	// Trigger the registered "after_update" handler with a report that has no
	// ActionTakenAt. The handler should *not* dispatch reportProcessedNotification.
	require.NotPanics(t, func() {
		event.Trigger("report", event.Event{
			Name: "after_update",
			Data: &models.Report{Id: 1, AccountId: alice.Id},
		})
	})
	// Give any (unexpected) goroutine a moment; then assert nothing changed.
	time.Sleep(50 * time.Millisecond)

	assert.Equal(t, before, countNotifications(t, gdb, alice.Id),
		"a report without ActionTakenAt must not create a notification")
}

func TestReportEvent_ActionTakenNotifies(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	gdb := setupTestDB(t)
	alice := createLocalAccount(t, gdb, "alice")

	now := time.Now()
	// The handler dispatches reportProcessedNotification in a goroutine.
	require.NotPanics(t, func() {
		event.Trigger("report", event.Event{
			Name: "after_update",
			Data: &models.Report{Id: 7, AccountId: alice.Id, ActionTakenAt: &now},
		})
	})

	require.Eventually(t, func() bool {
		return latestNotificationFor(t, gdb, alice.Id, models.NotificationTypeAdminReport) != nil
	}, time.Second, 10*time.Millisecond, "notification should appear after the handler runs")

	nf := latestNotificationFor(t, gdb, alice.Id, models.NotificationTypeAdminReport)
	require.NotNil(t, nf)
	assert.Equal(t, uint64(7), nf.ReportId)
}

// The report handler returns false for non-*Report data (skips dispatch).
func TestReportEvent_NonReportDataIsIgnored(t *testing.T) {
	notificationsStateMu.Lock()
	defer notificationsStateMu.Unlock()

	setupTestDB(t)

	// Passing the wrong type should not panic and should not dispatch.
	require.NotPanics(t, func() {
		event.Trigger("report", event.Event{
			Name: "after_update",
			Data: "not a report",
		})
	})
}
