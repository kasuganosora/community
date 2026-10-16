package models

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sort"
	"time"
)

type Poll struct { //nolint:recvcheck
	Id         uint64         `gorm:"primary_key;autoIncrement" json:"id"`
	AccountId  uint64         `gorm:"not null;index"             json:"account_id"`
	StatusId   uint64         `gorm:"not null;index"             json:"status_id"`
	ExpiresAt  *time.Time     `json:"expires_at"` // nil preserves polls without a closing time
	Multiple   bool           `gorm:"not null;default:false"     json:"multiple"`
	Options    PollOptions    `json:"options"`
	Properties map[string]any `gorm:"type:json;serializer:json"  json:"properties"`
	// ClosedNotifiedAt is set exactly once, when the poll-close worker has
	// sent the "poll" notifications (and, for local polls, the closing Update).
	ClosedNotifiedAt *time.Time     `gorm:"index:idx_polls_closed_notified_at" json:"-"`
	CreatedAt        *time.Time     `gorm:"not null;autoCreateTime"    json:"created_at"`
	UpdatedAt        *time.Time     `gorm:"not null;autoUpdateTime"    json:"updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"index"                      json:"-"`
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
	Id      uint64 `gorm:"primary_key;autoIncrement" json:"id"`
	PollId  uint64 `gorm:"not null;index"             json:"poll_id"`
	Index   uint8  `gorm:"type:tinyint;not null"                   json:"index"`
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
	Id        uint64     `gorm:"primary_key;autoIncrement" json:"id"`
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

// PollNoDeadlineSeconds is the expires_in stored for a "no deadline" poll.
// The Mastodon API has no open-ended polls, so they close in ~10 years.
const PollNoDeadlineSeconds = 10 * 365 * 24 * 3600

// IsClosed reports whether voting has ended (expiry passed or the remote
// author marked it closed).
func (p *Poll) IsClosed(now time.Time) bool {
	if p == nil {
		return false
	}
	if p.ExpiresAt != nil && !p.ExpiresAt.After(now) {
		return true
	}
	closed, _ := p.Properties["closed"].(bool)
	return closed
}

// RemoteVotersCount is the votersCount a remote server reported, if any.
func (p *Poll) RemoteVotersCount() uint64 {
	if p == nil || p.Properties == nil {
		return 0
	}
	switch v := p.Properties["voters_count"].(type) {
	case float64:
		if v > 0 {
			return uint64(v)
		}
	case int:
		if v > 0 {
			return uint64(v)
		}
	case int64:
		if v > 0 {
			return uint64(v)
		}
	case uint64:
		return v
	}
	return 0
}
