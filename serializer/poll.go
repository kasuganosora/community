package serializer

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"strconv"
	"time"
)

/**
{
              "id": "566",
              "expires_at": "2023-07-07T19:40:18.000Z",
              "expired": true,
              "multiple": false,
              "votes_count": 1568,
              "voters_count": 1568,
              "voted": true,
              "own_votes": [
                  0
              ],
              "options": [
                  {
                      "title": "bash",
                      "votes_count": 558
                  },
                  {
                      "title": "fish",
                      "votes_count": 231
                  },
                  {
                      "title": "tcsh",
                      "votes_count": 20
                  },
                  {
                      "title": "zsh",
                      "votes_count": 759
                  }
              ],
              "emojis": []
          }
*/

type Poll struct {
	Id          string       `json:"id"`
	ExpiresAt   *time.Time   `json:"expires_at"`
	Expired     bool         `json:"expired"`
	Multiple    bool         `json:"multiple"`
	VotesCount  uint64       `json:"votes_count"`
	VotersCount uint64       `json:"voters_count"`
	Voted       bool         `json:"voted"`
	OwnVotes    []uint64     `json:"own_votes"`
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
		Expired:     poll.ExpiresAt.Before(time.Now()),
		Multiple:    poll.Multiple,
		VotesCount:  votesCount,
		VotersCount: votersCount,
		Voted:       voted,
		OwnVotes:    []uint64{},
		Options:     []PollOption{},
		Emojis:      []Emoji{},
	}
	return ret
}

func ToPoll(poll *models.Poll, current *models.Account) (ret *Poll, err error) {
	optionsIndexMap := make(map[uint64]uint64)
	ret = ToBasePoll(poll, false, 0, 0)

	for _, option := range poll.Options {
		optionsIndexMap[option.Id] = option.Index
		ret.Options = append(ret.Options, PollOption{
			Title:      option.Content,
			VotesCount: option.Votes,
		})
	}

	var votesCount, votersCount int64

	err = db.GetDB().Model(&models.PollOptionVote{}).Where("poll_id = ?", poll.Id).Count(&votesCount).Error
	if err != nil {
		return
	}

	err = db.GetDB().Model(&models.PollOptionVote{}).Where("poll_id = ?", poll.Id).Distinct("account_id").Count(&votersCount).Error
	if err != nil {
		return
	}

	if current != nil {
		var myVotes []*models.PollOptionVote
		err = db.GetDB().Model(&models.PollOptionVote{}).Where("poll_id = ? AND account_id = ?", poll.Id, current.Id).Find(&myVotes).Error
		if err != nil {
			return
		}

		if len(myVotes) > 0 {
			ret.Voted = true
			for _, myVote := range myVotes {
				ret.OwnVotes = append(ret.OwnVotes, optionsIndexMap[myVote.OptionId])
			}
		}
	}

	return
}

type Counts struct {
	Cid   uint64
	Count int64
}

func ToPollsMapByStatusId(polls []*models.Poll, current *models.Account) (ret map[uint64]*Poll, err error) {
	// 一次过取出所有投票的选项
	optionsIndexMap := make(map[uint64]uint64)
	options := make([]*models.PollOption, 0)
	pollIds := make([]uint64, 0)
	ret = make(map[uint64]*Poll)

	for _, poll := range polls {
		pollIds = append(pollIds, poll.Id)
	}

	err = db.GetDB().Model(&models.PollOption{}).Where("poll_id IN (?)", pollIds).Find(&options).Error
	if err != nil {
		return
	}

	for _, option := range options {
		optionsIndexMap[option.Id] = option.Index
	}

	//  计算出所有已投票的票数和投票人数, 用投票ID来聚合
	votesCounts := make([]*Counts, 0)
	err = db.GetDB().Model(&models.PollOptionVote{}).Where("poll_id IN (?)", pollIds).Select("poll_id as cid, COUNT(*) AS count").Group("cid").Scan(&votesCounts).Error
	if err != nil {
		return
	}

	votesCountsMap := make(map[uint64]int64)
	for _, votesCount := range votesCounts {
		votesCountsMap[votesCount.Cid] = votesCount.Count
	}

	votersCounts := make([]*Counts, 0)
	err = db.GetDB().Model(&models.PollOptionVote{}).Where("poll_id IN (?)", pollIds).Select("poll_id as cid, COUNT(*) AS count").Group("concat(cid, '-' ,account_id)").Scan(&votersCounts).Error
	if err != nil {
		return
	}

	votersCountsMap := make(map[uint64]int64)
	for _, votersCount := range votersCounts {
		votersCountsMap[votersCount.Cid] = votersCount.Count
	}

	mySelectOptions := make([]*models.PollOptionVote, 0)
	if current != nil {
		err = db.GetDB().Model(&models.PollOptionVote{}).Where("poll_id IN (?) AND account_id = ?", pollIds, current.Id).Find(&mySelectOptions).Error
		if err != nil {
			return
		}
	}

	mySelectOptionsMap := make(map[uint64][]*models.PollOptionVote)
	for _, mySelectOption := range mySelectOptions {
		mySelectOptionsMap[mySelectOption.PollId] = append(mySelectOptionsMap[mySelectOption.PollId], mySelectOption)
	}

	for _, poll := range polls {
		voted := false
		var ownVotes []uint64
		if current != nil {
			if mySelOptions, ok := mySelectOptionsMap[poll.Id]; ok {
				voted = true
				for _, mySelectOption := range mySelOptions {
					ownVotes = append(ownVotes, optionsIndexMap[mySelectOption.OptionId])
				}
			}
		}

		ret[poll.Id] = ToBasePoll(poll, voted, uint64(votesCountsMap[poll.Id]), uint64(votersCountsMap[poll.Id]))
	}

	return
}
