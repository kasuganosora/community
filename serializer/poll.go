package serializer

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"strconv"
	"time"
)

type Poll struct {
	Id          string       `json:"id"`
	ExpiresAt   *time.Time   `json:"expires_at"`
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
		ExpiresAt:   poll.ExpiresAt,
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

func ToPoll(poll *models.Poll, current *models.Account) (ret *Poll, err error) {
	optionsIndexMap := make(map[uint64]uint8)
	ret = ToBasePoll(poll, false, 0, 0)

	for _, option := range poll.Options {
		optionsIndexMap[option.Id] = option.Index
		ret.Options = append(ret.Options, PollOption{
			Title:      option.Content,
			VotesCount: option.Votes,
		})
	}

	votes := make(models.PollOptionVotes, 0)

	err = db.GetDB().Model(&models.PollOptionVote{}).Where("poll_id = ?", poll.Id).Find(&votes).Error
	if err != nil {
		return ret, err
	}

	ret.VotesCount = uint64(len(votes))
	ret.VotersCount = uint64(len(votes.GetVotersAccountId()))

	if current != nil {
		myVotes := votes.GetVotedOptionIds(current.Id)

		if len(myVotes) > 0 {
			ret.Voted = true
			for _, myVotId := range myVotes {
				ret.OwnVotes = append(ret.OwnVotes, int(optionsIndexMap[myVotId]))
			}
		}
	}

	return ret, err
}

type Counts struct {
	Cid   uint64
	Count int64
}

func ToPollsMapByStatusId(polls []*models.Poll, current *models.Account) (ret map[uint64]*Poll, err error) {
	// 一次过取出所有投票的选项
	optionsIndexMap := make(map[uint64]uint8)
	pollIds := make([]uint64, 0, len(polls))
	ret = make(map[uint64]*Poll)

	for _, poll := range polls {
		pollIds = append(pollIds, poll.Id)

		for _, option := range poll.Options {
			optionsIndexMap[option.Id] = option.Index
		}
	}

	votes := make(models.PollOptionVotes, 0)

	err = db.GetDB().Model(&models.PollOptionVote{}).Where("poll_id in (?)", pollIds).Find(&votes).Error
	if err != nil {
		return ret, err
	}

	var mySelectOptions map[uint64]models.PollOptionVotes

	if current != nil {
		mySelectOptions = votes.FilterAccountId(current.Id).ToPollIdMap()
	}

	pollVoteMap := votes.ToPollIdMap()

	for _, poll := range polls {
		voted := false

		if current != nil {
			if _, ok := mySelectOptions[poll.Id]; ok {
				voted = true
			}
		}

		voteCounts := len(pollVoteMap[poll.Id])
		votersCount := len(pollVoteMap[poll.Id].GetVotersAccountId())

		ret[poll.Id] = ToBasePoll(poll, voted, uint64(voteCounts), uint64(votersCount))
	}

	return ret, err
}
