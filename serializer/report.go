package serializer

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm/clause"
	"strconv"
	"time"
)

type Report struct {
	Id             string     `json:"id"`
	Statuses       []*Status  `json:"statuses;omitempty"`
	ReportAccounts []*Account `json:"report_accounts;omitempty"`
	ReportType     uint16     `json:"report_type"`
	ReportContent  string     `json:"report_content;omitempty"`
	ResultComment  string     `json:"result_comment;omitempty"`
	Status         string     `json:"status"`
	ClosedAt       *time.Time `json:"closed_at;omitempty"`
	CreatedAt      *time.Time `json:"created_at;omitempty"`
	UpdatedAt      *time.Time `json:"updated_at;omitempty"`
}

func ToReport(r *models.Report, account *models.Account) (ret *Report, err error) {
	ret = &Report{}
	ret.Id = strconv.FormatUint(r.Id, 10)
	ret.ReportType = r.ReportType
	ret.ReportContent = r.ReportContent
	ret.ResultComment = r.ResultComment
	ret.ClosedAt = r.ClosedAt
	ret.CreatedAt = r.CreatedAt
	ret.UpdatedAt = r.UpdatedAt
	ret.Status = r.GetStatusString()

	if r.StatusIds != nil {
		ret.Statuses = make([]*Status, 0)
		statuses := make(models.Statuses, 0)
		err = models.StatusLoadAllQuery(db.GetDB().Model(&models.Status{})).
			Where("id in ?", r.StatusIds).Find(&ret.Statuses).Error

		if err != nil {
			return
		}

		ret.Statuses, err = ToStatuses(statuses, account)
		if err != nil {
			return
		}
	}

	if r.ReportAccountIds != nil {
		ret.ReportAccounts = make([]*Account, 0)
		accounts := make(models.Accounts, 0)
		err = db.GetDB().Model(&models.Account{}).Preload(clause.Associations).Where("id in ?", r.ReportAccountIds).Find(&accounts).Error
		if err != nil {
			return
		}

		for _, acc := range accounts {
			ret.ReportAccounts = append(ret.ReportAccounts, ToAccount(acc))
		}
	}

	return
}

func ToReports(rs []*models.Report, account *models.Account) (ret []*Report, err error) {
	ret = make([]*Report, 0)
	status := make([]*Status, 0)
	accounts := make([]*Account, 0)
	statusesId := make([]uint64, 0)
	accountsId := make([]uint64, 0)

	for _, r := range rs {
		if r.StatusIds != nil {
			statusesId = append(statusesId, r.StatusIds...)
		}
		if r.ReportAccountIds != nil {
			accountsId = append(accountsId, r.ReportAccountIds...)
		}
	}

	if len(statusesId) > 0 {
		_status := make(models.Statuses, 0)
		err = models.StatusLoadAllQuery(db.GetDB().Model(&models.Status{})).
			Where("id in ?", statusesId).Find(&_status).Error
		if err != nil {
			return
		}

		status, err = ToStatuses(_status, account)
		if err != nil {
			return
		}
	}

	if len(accountsId) > 0 {
		_accounts := make(models.Accounts, 0)
		err = db.GetDB().Model(&models.Account{}).Preload(clause.Associations).Where("id in ?", accountsId).Find(&_accounts).Error
		if err != nil {
			return
		}

		accounts = ToAccounts(_accounts)
	}

	statusesMap := utils.MapByAnyFiled(status, func(t *Status) uint64 {
		var id uint64
		id, _ = strconv.ParseUint(t.Id, 10, 64)
		return id
	})

	accountsMap := utils.MapByAnyFiled(accounts, func(t *Account) uint64 {
		var id uint64
		id, _ = strconv.ParseUint(t.Id, 10, 64)
		return id
	})

	ret = make([]*Report, 0)

	for _, r := range rs {
		report := &Report{}
		report.Id = strconv.FormatUint(r.Id, 10)
		report.ReportType = r.ReportType
		report.ReportContent = r.ReportContent
		report.ResultComment = r.ResultComment

		report.ClosedAt = r.ClosedAt
		report.CreatedAt = r.CreatedAt
		report.UpdatedAt = r.UpdatedAt
		report.Status = r.GetStatusString()

		if r.StatusIds != nil {
			report.Statuses = make([]*Status, 0)
			for _, id := range r.StatusIds {
				if s, ok := statusesMap[id]; ok {
					report.Statuses = append(report.Statuses, s)
				}
			}
		}

		if r.ReportAccountIds != nil {
			report.ReportAccounts = make([]*Account, 0)
			for _, id := range r.ReportAccountIds {
				if a, ok := accountsMap[id]; ok {
					report.ReportAccounts = append(report.ReportAccounts, a)
				}
			}
		}

		ret = append(ret, report)
	}
	return
}
