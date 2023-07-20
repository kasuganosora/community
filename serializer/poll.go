package serializer

import (
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

func ToPoll(poll *models.Poll, voted bool, votesCount uint64, votersCount uint64) *Poll {
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
