package models

import (
	"github.com/kasuganosora/community/event"
	"gorm.io/gorm"
	"time"
)

const (
	NotificationTypeMention = iota
	NotificationTypeReblog
	NotificationTypeFavourite
	NotificationTypeFollow
	NotificationTypePoll
	NotificationTypeFollowRequest
	NotificationTypeStatusUpdate
	NotificationTypeAdminReport
	NotificationTypeAddList    // 添加到列表
	NotificationTypeRemoveList // 从列表中移除
)

type Notification struct {
	Id            uint64 `gorm:"primaryKey;autoIncrement"`
	Type          uint8  `gorm:"type:tinyint;not null"`
	ActivityType  string `gorm:"index"`
	AccountId     uint64 `gorm:"index;not null"`
	FromAccountId uint64
	StatusId      uint64
	ReportId      uint64
	ListId        uint64

	CreatedAt *time.Time `gorm:"autoCreateTime"`
	UpdatedAt *time.Time `gorm:"autoUpdateTime"`

	Status      *Status  `gorm:"foreignKey:StatusId"`
	Account     *Account `gorm:"foreignKey:AccountId"`
	FromAccount *Account `gorm:"foreignKey:FromAccountId"`
	Report      *Report  `gorm:"foreignKey:ReportId"`
	List        *List    `gorm:"foreignKey:ListId"`
}

func (n *Notification) TableName() string {
	return "notifications"
}

// 绑定 增删改查 的事件钩子

func (n *Notification) BeforeCreate(tx *gorm.DB) (err error) {
	event.Trigger("notification", event.Event{
		Name: `before_create`,
		Data: n,
	})

	return
}

func (n *Notification) AfterCreate(tx *gorm.DB) (err error) {
	event.Trigger("notification", event.Event{
		Name: `after_create`, //nolint:goconst
		Data: n,
	})

	return
}

func (n *Notification) BeforeUpdate(tx *gorm.DB) (err error) {
	event.Trigger("notification", event.Event{
		Name: `before_update`,
		Data: n,
	})

	return
}

func (n *Notification) AfterUpdate(tx *gorm.DB) (err error) {
	event.Trigger("notification", event.Event{
		Name: `after_update`, //nolint:goconst
		Data: n,
	})

	return
}

func (n *Notification) BeforeDelete(tx *gorm.DB) (err error) {
	event.Trigger("notification", event.Event{
		Name: `before_delete`,
		Data: n,
	})

	return
}

func (n *Notification) AfterDelete(tx *gorm.DB) (err error) {
	event.Trigger("notification", event.Event{
		Name: `after_delete`,
		Data: n,
	})

	return
}

func (n *Notification) BeforeSave(tx *gorm.DB) (err error) {
	event.Trigger("notification", event.Event{
		Name: `before_save`, //nolint:goconst
		Data: n,
	})

	return
}

func (n *Notification) AfterSave(tx *gorm.DB) (err error) {
	event.Trigger("notification", event.Event{
		Name: `after_save`,
		Data: n,
	})

	return
}

func (n *Notification) GetTypeString() string {
	switch n.Type {
	case NotificationTypeMention:
		return "mention" //nolint:goconst
	case NotificationTypeReblog:
		return "reblog" //nolint:goconst
	case NotificationTypeFavourite:
		return "favourite" //nolint:goconst
	case NotificationTypeFollow:
		return "follow" //nolint:goconst
	case NotificationTypePoll:
		return "poll" //nolint:goconst
	case NotificationTypeFollowRequest:
		return "follow_request" //nolint:goconst
	case NotificationTypeStatusUpdate:
		return "status_update" //nolint:goconst
	case NotificationTypeAdminReport:
		return "admin_report" //nolint:goconst
	case NotificationTypeAddList:
		return "add_list" //nolint:goconst
	case NotificationTypeRemoveList:
		return "remove_list" //nolint:goconst
	default:
		return ""
	}
}

func (n *Notification) LoadAllExtendFields(db *gorm.DB) (err error) {
	if n.StatusId > 0 && n.Status == nil {
		n.Status = &Status{}
		err = db.Model(n).Association("Status").Find(n.Status)
		if err != nil {
			return err
		}
	}

	willBeLoadAccountsId := make([]uint64, 0)
	loadedAccounts := make(Accounts, 0)

	if n.AccountId > 0 && n.Account == nil {
		willBeLoadAccountsId = append(willBeLoadAccountsId, n.AccountId)
	}

	if n.FromAccountId > 0 && n.FromAccount == nil {
		willBeLoadAccountsId = append(willBeLoadAccountsId, n.FromAccountId)
	}

	if len(willBeLoadAccountsId) > 0 {
		err = db.Model(&Account{}).Preload("Emojis").Preload("Extra").Where("id in ?", willBeLoadAccountsId).Find(&loadedAccounts).Error
		if err != nil {
			return err
		}
	}

	if n.AccountId > 0 && n.Account == nil {
		for _, account := range loadedAccounts {
			if account.Id == n.AccountId {
				n.Account = account

				break
			}
		}
	}

	if n.FromAccountId > 0 && n.FromAccount == nil {
		for _, account := range loadedAccounts {
			if account.Id == n.FromAccountId {
				n.FromAccount = account

				break
			}
		}
	}

	if n.ReportId > 0 && n.Report == nil {
		n.Report = &Report{}
		err = db.Model(n).Association("Report").Find(n.Report)
		if err != nil {
			return err
		}
	}

	if n.ListId > 0 && n.List == nil {
		n.List = &List{}
		err = db.Model(n).Association("List").Find(n.List)
		if err != nil {
			return err
		}

		n.List.Creator = &Account{}
		err = db.Model(n.List).Association("Creator").Find(n.List.Creator)
		if err != nil {
			return err
		}
	}

	return err
}

type Notifications []*Notification
