package serializer

import (
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"testing"
)

func TestImportedPollAggregateDoesNotInventVoters(t *testing.T) {
	conn, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sql, err := conn.DB()
	require.NoError(t, err)
	t.Cleanup(func() { sql.Close() })
	require.NoError(t, conn.AutoMigrate(&models.PollOptionVote{}))
	poll := &models.Poll{Id: 1, StatusId: 2, Multiple: true, Options: models.PollOptions{{Id: 3, Index: 0, Votes: 7}, {Id: 4, Index: 1, Votes: 5}}}
	result, err := toPollsMapByStatusIdWithDB(conn, []*models.Poll{poll}, nil)
	require.NoError(t, err)
	require.EqualValues(t, 12, result[2].VotesCount)
	require.Zero(t, result[2].VotersCount)
	require.False(t, result[2].Voted)
	require.Empty(t, result[2].OwnVotes)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.PollOptionVote{PollId: 1, OptionId: 3, AccountId: 9}).Error)
	result, err = toPollsMapByStatusIdWithDB(conn, []*models.Poll{poll}, &models.Account{Id: 9})
	require.NoError(t, err)
	require.EqualValues(t, 12, result[2].VotesCount)
	require.EqualValues(t, 1, result[2].VotersCount)
	require.Equal(t, []int{0}, result[2].OwnVotes)
}
