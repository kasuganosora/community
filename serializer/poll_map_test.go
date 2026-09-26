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
