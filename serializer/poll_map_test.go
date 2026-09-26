package serializer

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestOwnVoteIndexes(t *testing.T) {
	idx := map[uint64]uint8{10: 0, 11: 1, 12: 2}
	votes := models.PollOptionVotes{{OptionId: 12}, {OptionId: 10}}
	assert.Equal(t, []int{2, 0}, ownVoteIndexes(votes, idx))
	assert.Equal(t, []int{}, ownVoteIndexes(nil, idx))
}

func TestToPollOptions_SortedByIndex(t *testing.T) {
	poll := &models.Poll{Options: models.PollOptions{
		{Index: 1, Content: "b", Votes: 2},
		{Index: 0, Content: "a", Votes: 5},
	}}
	assert.Equal(t, []PollOption{{Title: "a", VotesCount: 5}, {Title: "b", VotesCount: 2}}, toPollOptions(poll))
	// the model's slice is left untouched
	assert.Equal(t, "b", poll.Options[0].Content)
}
