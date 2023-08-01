package models

const (
	FollowPropertyKeyReblogs = "reblogs"
)

type Follow struct {
	Id         uint64         `json:"id" gorm:"primary_key;type:bigint(20);autoIncrement"`
	AccountId  uint64         `json:"account_id" gorm:"not null;type:bigint(20);index"`
	FollowId   uint64         `json:"follow_id" gorm:"not null;type:bigint(20);index"`
	Properties map[string]any `json:"properties" gorm:"type:json;serializer:json"`
	CreatedAt  int64          `json:"created_at" gorm:"type:timestamp;autoCreateTime"`
}

func (Follow) TableName() string {
	return "follows"
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
