package serializer

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"strconv"
	"time"
)

type Poll struct {
	Id          string       `json:"id"`
	ExpiresAt   *APITime     `json:"expires_at"`
	Expired     bool         `json:"expired"`
	Multiple    bool         `json:"multiple"`
	VotesCount  uint64       `json:"votes_count"`
	VotersCount uint64       `json:"voters_count"`
	Voted       bool         `json:"voted"`
	OwnVotes    []int        `json:"own_votes"`
	Options     []PollOption `json:"options"`
	Emojis      []Emoji      `json:"emojis"`
}

type PollOption struct {
	Title      string `json:"title"`
	VotesCount uint64 `json:"votes_count"`
}

func ToBasePoll(poll *models.Poll, voted bool, votesCount uint64, votersCount uint64) *Poll {
	id := strconv.FormatUint(poll.Id, 10)

	ret := &Poll{
		Id:          id,
		ExpiresAt:   NewAPITime(poll.ExpiresAt),
		Expired:     poll.ExpiresAt != nil && poll.ExpiresAt.Before(time.Now()),
		Multiple:    poll.Multiple,
		VotesCount:  votesCount,
		VotersCount: votersCount,
		Voted:       voted,
		OwnVotes:    []int{},
		Options:     []PollOption{},
		Emojis:      []Emoji{},
	}

	return ret
}

func ToPoll(poll *models.Poll, current *models.Account) (*Poll, error) {
	results, err := ToPollsMapByStatusId([]*models.Poll{poll}, current)
	if err != nil {
		return nil, err
	}
	return results[poll.StatusId], nil
}

type Counts struct {
	Cid   uint64
	Count int64
}

func ToPollsMapByStatusId(polls []*models.Poll, current *models.Account) (map[uint64]*Poll, error) {
	return toPollsMapByStatusIdWithDB(db.GetDB(), polls, current)
}
func toPollsMapByStatusIdWithDB(conn *gorm.DB, polls []*models.Poll, current *models.Account) (map[uint64]*Poll, error) {
	if conn != nil {
		if err := conn.Statement.Context.Err(); err != nil {
			return nil, err
		}
	}
	result := make(map[uint64]*Poll, len(polls))
	if len(polls) == 0 {
		return result, nil
	}
	ids := make([]uint64, 0, len(polls))
	seen := map[uint64]bool{}
	options := map[uint64]uint8{}
	for _, p := range polls {
		if !seen[p.Id] {
			ids = append(ids, p.Id)
			seen[p.Id] = true
		}
		for _, o := range p.Options {
			options[o.Id] = o.Index
		}
	}
	type total struct {
		PollID      uint64
		VotesCount  uint64
		VotersCount uint64
	}
	var totals []total
	if err := conn.Model(&models.PollOptionVote{}).Select("poll_id, COUNT(*) AS votes_count, COUNT(DISTINCT account_id) AS voters_count").Where("poll_id IN ?", ids).Group("poll_id").Scan(&totals).Error; err != nil {
		return nil, err
	}
	counts := map[uint64]total{}
	for _, c := range totals {
		counts[c.PollID] = c
	}
	mine := map[uint64]models.PollOptionVotes{}
	if current != nil {
		var votes models.PollOptionVotes
		if err := conn.Where("poll_id IN ? AND account_id = ?", ids, current.Id).Order("id ASC").Find(&votes).Error; err != nil {
			return nil, err
		}
		mine = votes.ToPollIdMap()
	}
	for _, p := range polls {
		c := counts[p.Id]
		own := mine[p.Id]
		// Imported polls may preserve aggregates without individual voter identities.
		var aggregate uint64
		for _, option := range p.Options {
			aggregate += option.Votes
		}
		if aggregate > c.VotesCount {
			c.VotesCount = aggregate
		}
		out := ToBasePoll(p, len(own) > 0, c.VotesCount, c.VotersCount)
		out.Options = toPollOptions(p)
		out.OwnVotes = ownVoteIndexes(own, options)
		result[p.StatusId] = out
	}
	return result, nil
}

// ownVoteIndexes maps the viewer's votes to option indexes (Mastodon's
// own_votes), in the order they were cast.
func ownVoteIndexes(myVotes models.PollOptionVotes, optionsIndexMap map[uint64]uint8) []int {
	ret := make([]int, 0, len(myVotes))
	for _, v := range myVotes {
		ret = append(ret, int(optionsIndexMap[v.OptionId]))
	}

	return ret
}

// toPollOptions serializes a poll's options in option-index order.
func toPollOptions(poll *models.Poll) []PollOption {
	options := append(models.PollOptions{}, poll.Options...).SortByIndex()

	ret := make([]PollOption, 0, len(options))
	for _, option := range options {
		ret = append(ret, PollOption{
			Title:      option.Content,
			VotesCount: option.Votes,
		})
	}

	return ret
}
