package models

import (
	"github.com/kasuganosora/community/event"
	"gorm.io/gorm"
)

const (
	FollowPropertyKeyReblogs = "reblogs"
)

const (
	FollowStatusFollowed  = iota
	FollowStatusRequested // 请求关注
)

type Follow struct { //nolint:recvcheck
	Id         uint64         `gorm:"primary_key;type:bigint(20);autoIncrement" json:"id"`
	AccountId  uint64         `gorm:"not null;type:bigint(20);index"            json:"account_id"`
	FollowId   uint64         `gorm:"not null;type:bigint(20);index"            json:"follow_id"`
	Status     int            `gorm:"not null;type:tinyint(4);default:0"        json:"status"`
	Properties map[string]any `gorm:"type:json;serializer:json"                 json:"properties"`
	CreatedAt  int64          `gorm:"type:timestamp;autoCreateTime"             json:"created_at"`
	Account    *Account       `gorm:"foreignKey:AccountId"                      json:"account"`
}

func (Follow) TableName() string {
	return "follows"
}

func (f *Follow) AfterCreate(tx *gorm.DB) (err error) {
	if f.Status == FollowStatusFollowed {
		event.Trigger("account", event.Event{
			Name: "follow", //nolint:goconst
			Data: f,
		})
	}

	if f.Status == FollowStatusRequested {
		event.Trigger("account", event.Event{
			Name: "follow-request",
			Data: f,
		})
	}

	return
}

func (f *Follow) AfterUpdate(tx *gorm.DB) (err error) {
	if f.Status == FollowStatusFollowed {
		event.Trigger("account", event.Event{
			Name: "follow",
			Data: f,
		})
	}

	return
}

func (f *Follow) AfterDelete(tx *gorm.DB) (err error) {
	event.Trigger("account", event.Event{
		Name: "unfollow",
		Data: f,
	})

	return
}

func Followed(db *gorm.DB) *gorm.DB {
	return db.Where("status = ?", FollowStatusFollowed)
}

func FollowRequested(db *gorm.DB) *gorm.DB {
	return db.Where("status = ?", FollowStatusRequested)
}

type Follows []*Follow

func (ss Follows) GetMapByFollowId() map[uint64][]*Follow {
	ret := make(map[uint64][]*Follow)
	for _, follow := range ss {
		if _, ok := ret[follow.FollowId]; !ok {
			ret[follow.FollowId] = make([]*Follow, 0)
		}

		ret[follow.FollowId] = append(ret[follow.FollowId], follow)
	}

	return ret
}

func (ss Follows) GetMapByAccountId() map[uint64][]*Follow {
	ret := make(map[uint64][]*Follow)
	for _, follow := range ss {
		if _, ok := ret[follow.AccountId]; !ok {
			ret[follow.AccountId] = make([]*Follow, 0)
		}

		ret[follow.AccountId] = append(ret[follow.AccountId], follow)
	}

	return ret
}

func (ss Follows) IsFollowed(accountId uint64, followId uint64) bool {
	for _, follow := range ss {
		if follow.AccountId == accountId && follow.FollowId == followId {
			return true
		}
	}

	return false
}

func (ss Follows) GetFollows(accountId uint64) []*Follow {
	ret := make([]*Follow, 0)

	for _, follow := range ss {
		if follow.AccountId == accountId {
			ret = append(ret, follow)
		}
	}

	return ret
}
