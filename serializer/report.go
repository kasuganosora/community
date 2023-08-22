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
	Id                   string     `json:"id"`
	ActionTaken          bool       `json:"action_taken"`
	ActionTakenAt        *time.Time `json:"action_taken_at"`
	Category             string     `json:"category"`
	Comment              string     `json:"comment"`
	Forwarded            bool       `json:"forwarded"`
	CreatedAt            *time.Time `json:"created_at"`
	StatusIds            []string   `json:"status_ids"`
	RuleIds              []string   `json:"rule_ids"`
	TargetAccount        *Account   `json:"target_account"`
	AssignedAccount      *Account   `json:"assigned_account"`
	ActionTakenByAccount *Account   `json:"action_taken_by_account"`
	Account              *Account   `json:"account"`
	Statuses             []*Status  `json:"statuses"`
	Rules                []*Rule    `json:"rules"`
}

func ToReport(r *models.Report, account *models.Account) (ret *Report, err error) {
	ret = &Report{}
	ret.Id = strconv.FormatUint(r.Id, 10)
	ret.ActionTaken = r.ActionTakenAt != nil
	ret.Comment = r.Comment
	ret.Category = r.ToCategoryString()
	ret.Forwarded = r.Forwarded
	ret.CreatedAt = r.CreatedAt
	ret.StatusIds = utils.NumberArrayToStingArray(r.StatusIds)
	ret.RuleIds = utils.NumberArrayToStingArray(r.RuleIds)

	preloadAccountsId := make([]uint64, 0)

	if r.TargetAccount == nil && r.TargetAccountId != 0 {
		preloadAccountsId = append(preloadAccountsId, r.TargetAccountId)
	}

	if r.Account == nil && r.AccountId != 0 {
		preloadAccountsId = append(preloadAccountsId, r.AccountId)
	}

	if r.ActionTakenByAccount == nil && r.ActionTakenByAccountId != 0 {
		preloadAccountsId = append(preloadAccountsId, r.ActionTakenByAccountId)
	}

	if r.AssignedAccount == nil && r.AssignedAccountId != 0 {
		preloadAccountsId = append(preloadAccountsId, r.AssignedAccountId)
	}

	if len(preloadAccountsId) > 0 {
		accounts := make([]*models.Account, 0)
		err = db.GetDB().Where("id IN ?", preloadAccountsId).Preload(clause.Associations).Find(&accounts).Error
		accountsMap := utils.MapByAnyFiled(accounts, func(account *models.Account) uint64 {
			return account.Id
		})

		if r.TargetAccount == nil && r.TargetAccountId != 0 && accountsMap[r.TargetAccountId] != nil {
			r.TargetAccount = accountsMap[r.TargetAccountId]
		}

		if r.Account == nil && r.AccountId != 0 {
			r.Account = accountsMap[r.AccountId]
		}

		if r.ActionTakenByAccount == nil && r.ActionTakenByAccountId != 0 {
			r.ActionTakenByAccount = accountsMap[r.ActionTakenByAccountId]
		}

		if r.AssignedAccount == nil && r.AssignedAccountId != 0 {
			r.AssignedAccount = accountsMap[r.AssignedAccountId]

		}
	}

	if r.TargetAccount != nil {
		ret.TargetAccount = ToAccount(r.TargetAccount)
	}

	if r.Account != nil {
		ret.Account = ToAccount(r.Account)
	}

	if r.ActionTakenByAccount != nil {
		ret.ActionTakenByAccount = ToAccount(r.ActionTakenByAccount)
	}

	if r.AssignedAccount != nil {
		ret.AssignedAccount = ToAccount(r.AssignedAccount)
	}

	if r.StatusIds != nil && len(r.StatusIds) > 0 {
		statuses := make([]*models.Status, 0)
		query := db.GetDB().Where("id IN ?", r.StatusIds)
		query = models.StatusLoadAllQuery(query)
		err = query.Find(&statuses).Error
		if err != nil {
			return
		}

		ret.Statuses, err = ToStatuses(statuses, account)
		if err != nil {
			return
		}
	} else {
		ret.Statuses = make([]*Status, 0)
	}

	if r.RuleIds != nil && len(r.RuleIds) > 0 {
		rules := make([]*models.Rule, 0)
		err = db.GetDB().Where("id IN ?", r.RuleIds).Find(&rules).Error
		if err != nil {
			return
		}

		ret.Rules = ToRules(rules)
	} else {
		ret.Rules = make([]*Rule, 0)
	}

	return
}

func ToReports(rs []*models.Report, account *models.Account) (ret []*Report, err error) {
	ret = make([]*Report, 0)
	status := make([]*Status, 0)
	accounts := make([]*Account, 0)
	accountsId := make([]uint64, 0)
	statusesId := make([]uint64, 0)
	rulesId := make([]uint64, 0)

	for _, r := range rs {
		if r.AccountId > 0 {
			accountsId = append(accountsId, r.AccountId)
		}

		if r.ActionTakenByAccountId > 0 {
			accountsId = append(accountsId, r.ActionTakenByAccountId)
		}

		if r.TargetAccountId > 0 {
			accountsId = append(accountsId, r.TargetAccountId)
		}

		if r.StatusIds != nil {
			statusesId = append(statusesId, r.StatusIds...)
		}

		if r.RuleIds != nil {
			rulesId = append(rulesId, r.RuleIds...)
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

	var rules []*Rule
	if len(rulesId) > 0 {
		_rules := make([]*models.Rule, 0)
		err = db.GetDB().Model(&models.Rule{}).Where("id in ?", rulesId).Find(&_rules).Error
		if err != nil {
			return
		}

		rules = ToRules(_rules)
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
		return t.intId
	})

	accountsMap := utils.MapByAnyFiled(accounts, func(t *Account) uint64 {
		return t.intId
	})

	routesMap := utils.MapByAnyFiled(rules, func(t *Rule) uint64 {
		return t.intId
	})

	ret = make([]*Report, 0)

	for _, r := range rs {
		report := &Report{}
		report.Id = strconv.FormatUint(r.Id, 10)
		report.ActionTaken = r.ActionTakenAt != nil
		report.ActionTakenAt = r.ActionTakenAt
		report.Category = r.ToCategoryString()
		report.Comment = r.Comment
		report.Forwarded = r.Forwarded
		report.CreatedAt = r.CreatedAt
		report.StatusIds = utils.NumberArrayToStingArray(r.StatusIds)
		report.RuleIds = utils.NumberArrayToStingArray(r.RuleIds)

		if r.TargetAccountId > 0 && accountsMap[r.TargetAccountId] != nil {
			report.TargetAccount = accountsMap[r.TargetAccountId]
		}

		if r.AccountId > 0 && accountsMap[r.AccountId] != nil {
			report.Account = accountsMap[r.AccountId]
		}

		if r.ActionTakenByAccountId > 0 && accountsMap[r.ActionTakenByAccountId] != nil {
			report.ActionTakenByAccount = accountsMap[r.ActionTakenByAccountId]
		}

		if r.AssignedAccountId > 0 && accountsMap[r.AssignedAccountId] != nil {
			report.AssignedAccount = accountsMap[r.AssignedAccountId]
		}

		if r.StatusIds != nil && len(r.StatusIds) > 0 {
			report.Statuses = make([]*Status, 0)
			for _, statusId := range r.StatusIds {
				if statusesMap[statusId] != nil {
					report.Statuses = append(report.Statuses, statusesMap[statusId])
				}
			}
		} else {
			report.Statuses = make([]*Status, 0)
		}

		if r.RuleIds != nil && len(r.RuleIds) > 0 {
			report.Rules = make([]*Rule, 0)
			for _, ruleId := range r.RuleIds {
				if routesMap[ruleId] != nil {
					report.Rules = append(report.Rules, routesMap[ruleId])
				}
			}
		} else {
			report.Rules = make([]*Rule, 0)
		}

		ret = append(ret, report)
	}
	return
}
