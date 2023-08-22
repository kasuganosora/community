package notifications

import (
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/models"
	"github.com/pkg/errors"
)

func init() {
	event.Register("report", "after_update", &event.Handler{Exec: func(event event.Event) bool {
		report := event.Data.(*models.Report)
		if report.ActionTakenAt != nil {
			go reportProcessedNotification(report)

		}
		return true
	}})
}

func reportProcessedNotification(report *models.Report) {
	nf := &models.Notification{
		Type:      models.NotificationTypeAdminReport,
		ReportId:  report.Id,
		AccountId: report.AccountId,
	}

	if err := db.GetDB().Create(nf).Error; err != nil {
		err = errors.Wrap(err, fmt.Sprintf("[reportProcessedNotification] create notification failed, reportId: %d", report.Id))
		return
	}
}
