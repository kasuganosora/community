package models

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sort"
	"time"
)

type Poll struct { //nolint:recvcheck
	Id         uint64         `gorm:"primary_key;auto_increment" json:"id"`
	AccountId  uint64         `gorm:"not null;index"             json:"account_id"`
	StatusId   uint64         `gorm:"not null;index"             json:"status_id"`
	ExpiresAt  *time.Time     `gorm:"not null"                   json:"expires_at"`
	Multiple   bool           `gorm:"not null;default:false"     json:"multiple"`
	Options    PollOptions    `json:"options"`
	Properties map[string]any `gorm:"type:json;serializer:json"  json:"properties"`
	CreatedAt  *time.Time     `gorm:"not null;autoCreateTime"    json:"created_at"`
	UpdatedAt  *time.Time     `gorm:"not null;autoUpdateTime"    json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index"                      json:"-"`
}

func (Poll) TableName() string {
	return "polls"
}

func (p *Poll) Delete(db *gorm.DB) (err error) {
	err = db.Select(clause.Associations).Delete(p).Error
	if err != nil {
		return
	}

	err = db.Model(&PollOptionVote{}).Where("poll_id = ?", p.Id).Delete(&PollOptionVote{}).Error
	if err != nil {
		return
	}

	err = db.Delete(p).Error

	return
}

type PollOption struct {
	Id      uint64 `gorm:"primary_key;auto_increment" json:"id"`
	PollId  uint64 `gorm:"not null;index"             json:"poll_id"`
	Index   uint8  `gorm:"not null"                   json:"index"`
	Content string `gorm:"not null"                   json:"content"`
	Votes   uint64 `gorm:"not null;default:0"         json:"votes"`
}

func (po *PollOption) TableName() string {
	return "poll_options"
}

type PollOptions []*PollOption

func (ss PollOptions) GetOptionIds() (ret []uint64) {
	for _, s := range ss {
		ret = append(ret, s.Id)
	}

	return
}

func (ss PollOptions) SortByIndex() PollOptions {
	sort.Slice(ss, func(i, j int) bool {
		return ss[i].Index < ss[j].Index
	})

	return ss
}

func (ss PollOptions) GetNames() (ret []string) {
	for _, s := range ss.SortByIndex() {
		ret = append(ret, s.Content)
	}

	return
}

type PollOptionVote struct {
	Id        uint64     `gorm:"primary_key;auto_increment" json:"id"`
	PollId    uint64     `gorm:"not null;index"             json:"poll_id"`
	AccountId uint64     `gorm:"not null;index"             json:"account_id"`
	OptionId  uint64     `gorm:"not null;index"             json:"option_id"`
	CreatedAt *time.Time `gorm:"not null;autoCreateTime"    json:"created_at"`
}

func (pov *PollOptionVote) TableName() string {
	return "poll_option_votes"
}

type PollOptionVotes []*PollOptionVote

func (ss PollOptionVotes) IsVoted(accountId uint64) bool {
	for _, s := range ss {
		if s.AccountId == accountId {
			return true
		}
	}

	return false
}

func (ss PollOptionVotes) GetVotedOptionIds(accountId uint64) (optionId []uint64) {
	for _, s := range ss {
		if s.AccountId == accountId {
			optionId = append(optionId, s.OptionId)
		}
	}

	return
}

func (ss PollOptionVotes) GetVotersAccountId() (ret []uint64) {
	exists := make(map[uint64]bool)

	for _, s := range ss {
		if !exists[s.AccountId] {
			exists[s.AccountId] = true
			ret = append(ret, s.AccountId)
		}
	}

	return
}

func (ss PollOptionVotes) FilterAccountId(accountId uint64) (ret PollOptionVotes) {
	for _, s := range ss {
		if s.AccountId == accountId {
			ret = append(ret, s)
		}
	}

	return
}

func (ss PollOptionVotes) FilterOptionId(optionId uint64) (ret PollOptionVotes) {
	for _, s := range ss {
		if s.OptionId == optionId {
			ret = append(ret, s)
		}
	}

	return
}

func (ss PollOptionVotes) FilterPollId(pollId uint64) (ret PollOptionVotes) {
	for _, s := range ss {
		if s.PollId == pollId {
			ret = append(ret, s)
		}
	}

	return
}

func (ss PollOptionVotes) ToAccountIdMap() (ret map[uint64]PollOptionVotes) {
	ret = make(map[uint64]PollOptionVotes)
	for _, s := range ss {
		if _, ok := ret[s.AccountId]; !ok {
			ret[s.AccountId] = PollOptionVotes{}
		}

		ret[s.AccountId] = append(ret[s.AccountId], s)
	}

	return
}

func (ss PollOptionVotes) ToOptionIdMap() (ret map[uint64]PollOptionVotes) {
	ret = make(map[uint64]PollOptionVotes)
	for _, s := range ss {
		if _, ok := ret[s.OptionId]; !ok {
			ret[s.OptionId] = PollOptionVotes{}
		}

		ret[s.OptionId] = append(ret[s.OptionId], s)
	}

	return
}

func (ss PollOptionVotes) ToPollIdMap() (ret map[uint64]PollOptionVotes) {
	ret = make(map[uint64]PollOptionVotes)
	for _, s := range ss {
		if _, ok := ret[s.PollId]; !ok {
			ret[s.PollId] = PollOptionVotes{}
		}

		ret[s.PollId] = append(ret[s.PollId], s)
	}

	return
}
