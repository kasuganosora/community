package models

import (
	"time"

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
	CreatedAt  *time.Time     `gorm:"type:timestamp;autoCreateTime"             json:"created_at"`
	Account    *Account       `gorm:"foreignKey:AccountId"                      json:"account"`
}

func (Follow) TableName() string {
	return "follows"
}

func (f *Follow) AfterCreate(tx *gorm.DB) (err error) {
	if err = queueFederationFollow(tx, "Follow", f); err != nil {
		return err
	}
	if f.Status == FollowStatusFollowed {
		event.Trigger("account", event.Event{
			Name: "follow", //nolint:goconst
			Data: f,
		})

		// 直接关注（非请求态）：维护关注者/被关注者的计数。
		// 注意：申请关注（FollowStatusRequested）不计入计数，待被关注者批准后
		// 由 follow_request.Authorize 负责递增，避免在 Hook 与 Controller 中重复计数。
		err = updateFollowCounters(tx, f.AccountId, f.FollowId, +1)
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
	if err = queueFederationFollow(tx, "Undo", f); err != nil {
		return err
	}
	event.Trigger("account", event.Event{
		Name: "unfollow",
		Data: f,
	})

	// 只有处于「已关注」状态的关系被删除时才需要递减计数；
	// 处于「申请中」状态的关系删除（例如拒绝/撤回申请）不应影响计数。
	if f.Status == FollowStatusFollowed {
		err = updateFollowCounters(tx, f.AccountId, f.FollowId, -1)
	}

	return
}

// updateFollowCounters 原子地维护 AccountExtra 中的 FollowingCount/FollowersCount。
// delta 为 +1（建立关注）或 -1（取消关注）。
// 这些列为有符号 int（int(11)），因此直接使用加减法即可，无需 if() 守卫，
// 也避免了对不支持 UPDATE ... SET col=func() 的数据库（如测试用 TiDB）的依赖。
// accountId 为发起关注的账户（其 FollowingCount 变化），followId 为被关注的账户（其 FollowersCount 变化）。
//
// 计数器属于派生的辅助数据，其更新失败不应导致「关注」这一核心操作回滚。
// 因此当 accounts_extra 表尚未创建（如引导阶段、最小化测试 schema）时，跳过计数器更新而不报错；
// 真正的数据库错误会通过返回值向上传递（在生产环境 accounts_extra 始终存在）。
func updateFollowCounters(tx *gorm.DB, accountId, followId uint64, delta int) (err error) {
	if accountId == 0 || followId == 0 {
		return
	}

	// accounts_extra 表可能尚未迁移（测试 / 引导场景），此时跳过计数器维护。
	if !tx.Migrator().HasTable(&AccountExtra{}) {
		return
	}

	err = tx.Model(&AccountExtra{}).Where("account_id = ?", accountId).
		UpdateColumn("following_count", gorm.Expr("following_count + ?", delta)).Error
	if err != nil {
		return
	}

	err = tx.Model(&AccountExtra{}).Where("account_id = ?", followId).
		UpdateColumn("followers_count", gorm.Expr("followers_count + ?", delta)).Error

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
