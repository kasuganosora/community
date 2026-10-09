package models

import (
	"time"

	"github.com/kasuganosora/community/db/dialect"
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

// DeleteFollowEdges removes both directions of a follow so AfterDelete
// can decrement followers_count and following_count. A bulk Where().Delete
// skips those hooks and leaves the profile counters stale.

// ReconcileFollowCounts rewrites local profile counters from accepted follow
// rows. Bulk deletes used to skip the follow hook, so a profile could show a
// follower count while the list was empty. Remote accounts keep the totals
// their server publishes (followers/following collection totalItems).
func ReconcileFollowCounts(conn *gorm.DB) error {
	if !conn.Migrator().HasTable(&AccountExtra{}) || !conn.Migrator().HasTable(&Follow{}) {
		return nil
	}
	if !dialect.IsMySQL(conn) {
		// UPDATE ... JOIN is MySQL-only; correlated subqueries work on
		// PostgreSQL and SQLite.
		err := conn.Exec(`UPDATE accounts_extra SET following_count = (
			SELECT COUNT(*) FROM follows WHERE follows.account_id = accounts_extra.account_id AND follows.status = ?)
			WHERE account_id IN (SELECT id FROM accounts WHERE domain IS NULL)`, FollowStatusFollowed).Error
		if err != nil {
			return err
		}
		return conn.Exec(`UPDATE accounts_extra SET followers_count = (
			SELECT COUNT(*) FROM follows WHERE follows.follow_id = accounts_extra.account_id AND follows.status = ?)
			WHERE account_id IN (SELECT id FROM accounts WHERE domain IS NULL)`, FollowStatusFollowed).Error
	}
	err := conn.Exec(`UPDATE accounts_extra e
		LEFT JOIN (
			SELECT account_id, COUNT(*) AS c FROM follows WHERE status = ? GROUP BY account_id
		) f ON f.account_id = e.account_id
		JOIN accounts a ON a.id = e.account_id AND a.domain IS NULL
		SET e.following_count = COALESCE(f.c, 0)`, FollowStatusFollowed).Error
	if err != nil {
		return err
	}
	return conn.Exec(`UPDATE accounts_extra e
		LEFT JOIN (
			SELECT follow_id, COUNT(*) AS c FROM follows WHERE status = ? GROUP BY follow_id
		) f ON f.follow_id = e.account_id
		JOIN accounts a ON a.id = e.account_id AND a.domain IS NULL
		SET e.followers_count = COALESCE(f.c, 0)`, FollowStatusFollowed).Error
}

func DeleteFollow(conn *gorm.DB, accountId, followId uint64) error {
	if accountId == 0 || followId == 0 {
		return nil
	}
	var follows []Follow
	err := conn.Where("account_id = ? AND follow_id = ?", accountId, followId).Find(&follows).Error
	if err != nil {
		return err
	}
	for i := range follows {
		if err = conn.Delete(&follows[i]).Error; err != nil {
			return err
		}
	}
	return nil
}

func DeleteFollowEdges(conn *gorm.DB, a, b uint64) error {
	if err := DeleteFollow(conn, a, b); err != nil {
		return err
	}
	return DeleteFollow(conn, b, a)
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
