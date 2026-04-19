package serializer

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"strconv"
	"time"
)

type Notification struct {
	Id        string     `json:"id"`
	Type      string     `json:"type"`
	CreatedAt *time.Time `json:"created_at"`
	Account   *Account   `json:"account,omitempty"`
	Status    *Status    `json:"status,omitempty"`
	Report    *Report    `json:"report,omitempty"`
	List      *List      `json:"list,omitempty"`
}

func ToNotification(n *models.Notification, currentAccount *models.Account) (ret *Notification, err error) {
	ret = &Notification{}
	ret.Id = strconv.FormatUint(n.Id, 10)
	ret.Type = n.GetTypeString()
	ret.CreatedAt = n.CreatedAt

	if n.FromAccountId != 0 && n.FromAccount == nil {
		fromAccount := &models.Account{}
		err = db.GetDB().Model(&models.Account{}).Preload("Emojis").Preload("Extra").First(fromAccount, n.FromAccountId).Error
		if err != nil {
			return
		}

		ret.Account = ToAccount(fromAccount)
	} else {
		ret.Account = ToAccount(n.FromAccount)
	}

	if n.StatusId > 0 && n.Status == nil {
		n.Status = &models.Status{}

		err = db.GetDB().Model(&models.Status{}).Find(n.Status, n.StatusId).Error
		if err != nil {
			return
		}
	}

	if err = n.Status.CheckAndLoadAllExtendFields(db.GetDB()); err != nil {
		return
	}

	ret.Status, err = ToStatus(n.Status, currentAccount)
	if err != nil {
		return
	}

	if n.ReportId > 0 && n.Report == nil {
		n.Report = &models.Report{}
		err = db.GetDB().Model(&models.Report{}).Find(n.Report, n.ReportId).Error
		if err != nil {
			return
		}
	}

	ret.Report, err = ToReport(n.Report, currentAccount)

	if n.ListId > 0 && n.List == nil {
		n.List = &models.List{}
		err = db.GetDB().Model(&models.List{}).Preload("Creator").Find(n.List, n.ListId).Error
		if err != nil {
			return
		}
	}

	return
}

func ToNotifications(ns []*models.Notification, currentAccount *models.Account) (ret []*Notification, err error) {
	accountIds := make([]uint64, 0)
	statusIds := make([]uint64, 0)
	reportsIds := make([]uint64, 0)
	listsIds := make([]uint64, 0)

	for _, n := range ns {
		if n.FromAccountId != 0 {
			accountIds = append(accountIds, n.FromAccountId)
		}
		if n.StatusId != 0 {
			statusIds = append(statusIds, n.StatusId)
		}

		if n.ReportId != 0 {
			reportsIds = append(reportsIds, n.ReportId)
		}
	}

	var accounts []*Account
	_accounts := make(models.Accounts, 0)
	err = db.GetDB().Model(&models.Account{}).Preload("Emojis").Preload("Extra").Where("id in ?", accountIds).Find(&_accounts).Error
	if err != nil {
		return
	}

	accounts = ToAccounts(_accounts)

	statuses := make([]*Status, 0)
	if len(statusIds) > 0 {
		_statuses := make(models.Statuses, 0)
		err = models.StatusLoadAllQuery(db.GetDB().Model(&models.Status{})).
			Where("id in ?", statusIds).Find(&_statuses).Error

		if err != nil {
			return
		}

		statuses, err = ToStatuses(_statuses, currentAccount)
		if err != nil {
			return
		}
	}

	reports := make([]*Report, 0)
	if len(reportsIds) > 0 {
		_reports := make(models.Reports, 0)
		err = db.GetDB().Model(&models.Report{}).Where("id in ?", reportsIds).Find(&_reports).Error
		if err != nil {
			return
		}

		reports, err = ToReports(_reports, currentAccount)
		if err != nil {
			return
		}
	}

	lists := make([]*List, 0)
	if len(listsIds) > 0 {
		_lists := make([]*models.List, 0)
		err = db.GetDB().Model(&models.List{}).Preload("Creator").Where("id in ?", listsIds).Find(&_lists).Error
		if err != nil {
			return
		}

		lists = ToLists(_lists)
		if err != nil {
			return
		}
	}

	accountsMap := utils.MapByAnyFiled(accounts, func(t *Account) uint64 {
		var id uint64
		id, _ = strconv.ParseUint(t.Id, 10, 64)
		return id
	})

	statusesMap := utils.MapByAnyFiled(statuses, func(t *Status) uint64 {
		var id uint64
		id, _ = strconv.ParseUint(t.Id, 10, 64)
		return id
	})

	reportsMap := utils.MapByAnyFiled(reports, func(t *Report) uint64 {
		var id uint64
		id, _ = strconv.ParseUint(t.Id, 10, 64)
		return id
	})

	listsMap := utils.MapByAnyFiled(lists, func(t *List) uint64 {
		var id uint64
		id, _ = strconv.ParseUint(t.Id, 10, 64)
		return id
	})

	ret = make([]*Notification, 0)

	for _, n := range ns {
		nf := &Notification{}
		nf.Id = strconv.FormatUint(n.Id, 10)
		nf.Type = n.GetTypeString()
		nf.CreatedAt = n.CreatedAt

		if n.FromAccountId != 0 {
			if account, ok := accountsMap[n.FromAccountId]; ok {
				nf.Account = account
			}
		}

		if n.StatusId != 0 {
			if status, ok := statusesMap[n.StatusId]; ok {
				nf.Status = status
			}
		}

		if n.ReportId != 0 {
			if report, ok := reportsMap[n.ReportId]; ok {
				nf.Report = report
			}
		}

		if n.ListId != 0 {
			if list, ok := listsMap[n.ListId]; ok {
				nf.List = list
			}
		}

		ret = append(ret, nf)
	}

	return
}
