package models

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/pkg/errors"
	"gorm.io/gorm"
	"time"
)

const (
	ListRepliesPolicyList     = iota // 仅显示列表成员之间的回复
	ListRepliesPolicyFollowed        // 显示与列表成员相关的所有回复，包括列表成员回复给其他非列表成员的回复
	ListRepliesPolicyNone            // 不显示任何回复
)

const (
	ListVisibilityPublic = iota
	ListVisibilityPrivate
	ListVisibilityFollowers // 关注了的人才可以看列表里的status,和这个列表包含的人
)

type List struct { //nolint:recvcheck
	Id            uint64         `gorm:"primary_key;autoIncrement"      json:"id"`
	Title         string         `gorm:"type:varchar(255);not null"      json:"title"`
	AccountId     uint64         `gorm:"not null"                        json:"account_id"`
	RepliesPolicy uint8          `gorm:"not null;default:0"              json:"replies_policy"`
	LastViewedAt  *time.Time     `gorm:"index"                           json:"last_viewed_at"` // 最后被查看的时间
	Visibility    int            `gorm:"not null;default:0"              json:"visibility"`
	CreatedAt     *time.Time     `gorm:"autoCreateTime"                  json:"created_at"`
	UpdatedAt     *time.Time     `gorm:"autoUpdateTime"                  json:"updated_at"`
	DeletedAt     gorm.DeletedAt `gorm:"index"                           json:"-"`
	Accounts      []*Account     `gorm:"many2many:list_accounts;"        json:"accounts"`
	Followers     []*Account     `gorm:"many2many:list_follow_accounts;" json:"followers"`
	Creator       *Account       `gorm:"foreignKey:AccountId"            json:"creator"`
}

func (List) TableName() string {
	return "lists"
}

func (l *List) AfterCreate(tx *gorm.DB) (err error) {
	listFollowAccount := &ListFollowAccount{
		ListId:    l.Id,
		AccountId: l.AccountId,
	}

	return tx.Create(listFollowAccount).Error
}

func (l *List) SetRepliesPolicy(policy string) {
	switch policy {
	case "list": //nolint:goconst
		l.RepliesPolicy = ListRepliesPolicyList
	case "followed":
		l.RepliesPolicy = ListRepliesPolicyFollowed
	case "none": //nolint:goconst
		l.RepliesPolicy = ListRepliesPolicyNone
	default:
		l.RepliesPolicy = ListRepliesPolicyList
	}
}

func (l *List) GetRepliesPolicy() string {
	switch l.RepliesPolicy {
	case ListRepliesPolicyList:
		return "list"
	case ListRepliesPolicyFollowed:
		return "followed"
	case ListRepliesPolicyNone:
		return "none"
	default:
		return "list"
	}
}

type ListAccount struct {
	ID        uint64     `gorm:"primary_key;autoIncrement" json:"id"`
	ListId    uint64     `gorm:"not null;index"             json:"list_id"`
	AccountId uint64     `gorm:"not null;index"             json:"account_id"`
	CreatedAt *time.Time `gorm:"autoCreateTime"             json:"created_at"`
}

func (la *ListAccount) TableName() string {
	return "list_accounts"
}

func (la *ListAccount) AfterCreate(tx *gorm.DB) (err error) {
	if err = queueFeedListAccount(tx, la); err != nil {
		return err
	}
	event.Trigger("list", event.Event{
		Name: "add-to-list",
		Data: la,
	})

	return
}

func (la *ListAccount) AfterDelete(tx *gorm.DB) (err error) {
	event.Trigger("list", event.Event{
		Name: "remove-from-list",
		Data: la,
	})

	return
}

type ListFollowAccount struct {
	ID        uint64     `gorm:"primary_key;autoIncrement" json:"id"`
	ListId    uint64     `gorm:"not null;index"             json:"list_id"`
	AccountId uint64     `gorm:"not null;index"             json:"account_id"`
	CreatedAt *time.Time `gorm:"autoCreateTime"             json:"created_at"`
}

func (lfa *ListFollowAccount) TableName() string {
	return "list_follow_accounts"
}

func (lfa *ListFollowAccount) AfterCreate(tx *gorm.DB) (err error) {
	if err = QueueFeedInvalidation(tx, FeedEventMetadata{ActorID: lfa.AccountId, RecipientIDs: []uint64{lfa.AccountId}, ListIDs: []uint64{lfa.ListId}, Reason: "list_subscription_changed"}); err != nil {
		return err
	}
	event.Trigger("list", event.Event{
		Name: "follow-list",
		Data: lfa,
	})

	return
}

func (lfa *ListFollowAccount) AfterDelete(tx *gorm.DB) (err error) {
	event.Trigger("list", event.Event{
		Name: "unfollow-list",
		Data: lfa,
	})

	return
}

func ListCanView(list *List, account *Account) (pass bool, err error) {
	if list.Visibility == ListVisibilityPublic {
		pass = true

		return pass, err
	}

	if list.AccountId == account.Id {
		pass = true

		return pass, err
	}

	// 其他的必须是关注了列表的人才能看
	if list.Followers != nil {
		for _, follower := range list.Followers {
			if follower.Id == account.Id {
				pass = true

				return pass, err
			}
		}

		return pass, err
	}

	err = db.GetDB().Model(&ListFollowAccount{}).Where("list_id = ? AND account_id = ?", list.Id, account.Id).First(&ListFollowAccount{}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = nil
			pass = false
		}

		return pass, err
	}

	// Follower record found — grant access
	pass = true

	return pass, err
}

// MaxListHashtags bounds how many hashtags one list may follow.
const MaxListHashtags = 20

// ListHashtag makes a list also collect public/unlisted posts carrying a tag
// (like a Misskey antenna). tag_id is indexed for tag -> lists fan-out.
type ListHashtag struct {
	Id        uint64     `gorm:"primary_key;autoIncrement"                                   json:"id"`
	ListId    uint64     `gorm:"not null;uniqueIndex:uidx_list_hashtags_list_tag,priority:1" json:"list_id"`
	TagId     uint64     `gorm:"not null;index;uniqueIndex:uidx_list_hashtags_list_tag,priority:2" json:"tag_id"`
	CreatedAt *time.Time `gorm:"autoCreateTime"                                              json:"created_at"`
}

func (ListHashtag) TableName() string { return "list_hashtags" }

// ListAuthorBlockedSQL is true when the list owner (first and second ?) and
// the status author have blocked each other, or the owner blocked the
// author's domain (third ?). Strangers reached through a list hashtag must
// pass it; members were added by hand.
const ListAuthorBlockedSQL = `(EXISTS (SELECT 1 FROM blocks WHERE blocks.type = 0 AND ((blocks.account_id = ? AND blocks.target_account_id = statuses.account_id) OR (blocks.account_id = statuses.account_id AND blocks.target_account_id = ?)))` +
	` OR (statuses.domain IS NOT NULL AND EXISTS (SELECT 1 FROM blocks WHERE blocks.type = 1 AND blocks.account_id = ? AND blocks.target_domain = statuses.domain)))`
