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
	Type          uint8  `gorm:"not null"`
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
		Name: `after_create`,
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
		Name: `after_update`,
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
		Name: `before_save`,
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
		return "mention"
	case NotificationTypeReblog:
		return "reblog"
	case NotificationTypeFavourite:
		return "favourite"
	case NotificationTypeFollow:
		return "follow"
	case NotificationTypePoll:
		return "poll"
	case NotificationTypeFollowRequest:
		return "follow_request"
	case NotificationTypeStatusUpdate:
		return "status_update"
	case NotificationTypeAdminReport:
		return "admin_report"
	case NotificationTypeAddList:
		return "add_list"
	case NotificationTypeRemoveList:
		return "remove_list"
	default:
		return ""
	}
}

func (n *Notification) LoadAllExtendFields(db *gorm.DB) (err error) {
	if n.StatusId > 0 && n.Status == nil {
		n.Status = &Status{}
		if err = db.Model(n).Association("Status").Find(n.Status); err != nil {
			return
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
		if err = db.Model(&Account{}).Preload("Emojis").Preload("Extra").Where("id in ?", willBeLoadAccountsId).Find(&loadedAccounts).Error; err != nil {
			return
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
		if err = db.Model(n).Association("Report").Find(n.Report); err != nil {
			return
		}
	}

	if n.ListId > 0 && n.List == nil {
		n.List = &List{}
		if err = db.Model(n).Association("List").Find(n.List); err != nil {
			return
		}

		n.List.Creator = &Account{}
		if err = db.Model(n.List).Association("Creator").Find(n.List.Creator); err != nil {
			return
		}
	}

	return
}

type Notifications []*Notification
