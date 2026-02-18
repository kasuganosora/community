package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPollOptions_GetOptionIds(t *testing.T) {
	options := PollOptions{
		{Id: 1, Index: 0, Content: "Option A"},
		{Id: 2, Index: 1, Content: "Option B"},
		{Id: 3, Index: 2, Content: "Option C"},
	}

	ids := options.GetOptionIds()
	assert.Len(t, ids, 3)
	assert.Equal(t, []uint64{1, 2, 3}, ids)
}

func TestPollOptions_SortByIndex(t *testing.T) {
	options := PollOptions{
		{Id: 3, Index: 2, Content: "Option C"},
		{Id: 1, Index: 0, Content: "Option A"},
		{Id: 2, Index: 1, Content: "Option B"},
	}

	sorted := options.SortByIndex()
	assert.Len(t, sorted, 3)
	assert.Equal(t, uint8(0), sorted[0].Index)
	assert.Equal(t, uint8(1), sorted[1].Index)
	assert.Equal(t, uint8(2), sorted[2].Index)
}

func TestPollOptions_GetNames(t *testing.T) {
	options := PollOptions{
		{Id: 3, Index: 2, Content: "Option C"},
		{Id: 1, Index: 0, Content: "Option A"},
		{Id: 2, Index: 1, Content: "Option B"},
	}

	names := options.GetNames()
	assert.Len(t, names, 3)
	assert.Equal(t, []string{"Option A", "Option B", "Option C"}, names)
}

func TestPollOptionVotes_IsVoted(t *testing.T) {
	votes := PollOptionVotes{
		{Id: 1, PollId: 1, AccountId: 100, OptionId: 1},
		{Id: 2, PollId: 1, AccountId: 101, OptionId: 2},
		{Id: 3, PollId: 1, AccountId: 100, OptionId: 3},
	}

	t.Run("User has voted", func(t *testing.T) {
		assert.True(t, votes.IsVoted(100))
		assert.True(t, votes.IsVoted(101))
	})

	t.Run("User has not voted", func(t *testing.T) {
		assert.False(t, votes.IsVoted(999))
	})
}

func TestPollOptionVotes_GetVotedOptionIds(t *testing.T) {
	votes := PollOptionVotes{
		{Id: 1, PollId: 1, AccountId: 100, OptionId: 1},
		{Id: 2, PollId: 1, AccountId: 101, OptionId: 2},
		{Id: 3, PollId: 1, AccountId: 100, OptionId: 3},
	}

	t.Run("Get voted option IDs for user", func(t *testing.T) {
		optionIds := votes.GetVotedOptionIds(100)
		assert.Len(t, optionIds, 2)
		assert.Contains(t, optionIds, uint64(1))
		assert.Contains(t, optionIds, uint64(3))
	})

	t.Run("User with no votes", func(t *testing.T) {
		optionIds := votes.GetVotedOptionIds(999)
		assert.Len(t, optionIds, 0)
	})
}

func TestPollOptionVotes_GetVotersAccountId(t *testing.T) {
	votes := PollOptionVotes{
		{Id: 1, PollId: 1, AccountId: 100, OptionId: 1},
		{Id: 2, PollId: 1, AccountId: 101, OptionId: 2},
		{Id: 3, PollId: 1, AccountId: 100, OptionId: 3}, // Same user, different option
	}

	voterIds := votes.GetVotersAccountId()
	assert.Len(t, voterIds, 2) // Should deduplicate
	assert.Contains(t, voterIds, uint64(100))
	assert.Contains(t, voterIds, uint64(101))
}

func TestPollOptionVotes_FilterAccountId(t *testing.T) {
	votes := PollOptionVotes{
		{Id: 1, PollId: 1, AccountId: 100, OptionId: 1},
		{Id: 2, PollId: 1, AccountId: 101, OptionId: 2},
		{Id: 3, PollId: 1, AccountId: 100, OptionId: 3},
	}

	t.Run("Filter by account ID", func(t *testing.T) {
		filtered := votes.FilterAccountId(100)
		assert.Len(t, filtered, 2)
		for _, v := range filtered {
			assert.Equal(t, uint64(100), v.AccountId)
		}
	})

	t.Run("No matching account", func(t *testing.T) {
		filtered := votes.FilterAccountId(999)
		assert.Len(t, filtered, 0)
	})
}

func TestPollOptionVotes_FilterOptionId(t *testing.T) {
	votes := PollOptionVotes{
		{Id: 1, PollId: 1, AccountId: 100, OptionId: 1},
		{Id: 2, PollId: 1, AccountId: 101, OptionId: 2},
		{Id: 3, PollId: 2, AccountId: 102, OptionId: 1},
	}

	t.Run("Filter by option ID", func(t *testing.T) {
		filtered := votes.FilterOptionId(1)
		assert.Len(t, filtered, 2)
		for _, v := range filtered {
			assert.Equal(t, uint64(1), v.OptionId)
		}
	})
}

func TestPollOptionVotes_FilterPollId(t *testing.T) {
	votes := PollOptionVotes{
		{Id: 1, PollId: 1, AccountId: 100, OptionId: 1},
		{Id: 2, PollId: 1, AccountId: 101, OptionId: 2},
		{Id: 3, PollId: 2, AccountId: 102, OptionId: 3},
	}

	t.Run("Filter by poll ID", func(t *testing.T) {
		filtered := votes.FilterPollId(1)
		assert.Len(t, filtered, 2)
		for _, v := range filtered {
			assert.Equal(t, uint64(1), v.PollId)
		}
	})
}

func TestPollOptionVotes_ToAccountIdMap(t *testing.T) {
	now := time.Now()
	votes := PollOptionVotes{
		{Id: 1, PollId: 1, AccountId: 100, OptionId: 1, CratedAt: &now},
		{Id: 2, PollId: 1, AccountId: 100, OptionId: 2, CratedAt: &now},
		{Id: 3, PollId: 1, AccountId: 101, OptionId: 1, CratedAt: &now},
	}

	accountMap := votes.ToAccountIdMap()
	assert.Len(t, accountMap, 2)
	assert.Len(t, accountMap[100], 2)
	assert.Len(t, accountMap[101], 1)
}

func TestPollOptionVotes_ToOptionIdMap(t *testing.T) {
	now := time.Now()
	votes := PollOptionVotes{
		{Id: 1, PollId: 1, AccountId: 100, OptionId: 1, CratedAt: &now},
		{Id: 2, PollId: 1, AccountId: 101, OptionId: 1, CratedAt: &now},
		{Id: 3, PollId: 1, AccountId: 102, OptionId: 2, CratedAt: &now},
	}

	optionMap := votes.ToOptionIdMap()
	assert.Len(t, optionMap, 2)
	assert.Len(t, optionMap[1], 2)
	assert.Len(t, optionMap[2], 1)
}

func TestPollOptionVotes_ToPollIdMap(t *testing.T) {
	now := time.Now()
	votes := PollOptionVotes{
		{Id: 1, PollId: 1, AccountId: 100, OptionId: 1, CratedAt: &now},
		{Id: 2, PollId: 1, AccountId: 101, OptionId: 2, CratedAt: &now},
		{Id: 3, PollId: 2, AccountId: 102, OptionId: 3, CratedAt: &now},
	}

	pollMap := votes.ToPollIdMap()
	assert.Len(t, pollMap, 2)
	assert.Len(t, pollMap[1], 2)
	assert.Len(t, pollMap[2], 1)
}

func TestPoll_TableName(t *testing.T) {
	poll := Poll{}
	assert.Equal(t, "polls", poll.TableName())
}

func TestPollOption_TableName(t *testing.T) {
	option := PollOption{}
	assert.Equal(t, "poll_options", option.TableName())
}

func TestPollOptionVote_TableName(t *testing.T) {
	vote := PollOptionVote{}
	assert.Equal(t, "poll_option_votes", vote.TableName())
}
