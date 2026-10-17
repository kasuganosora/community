package activitypub

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Status 35873: a closed Misskey anyOf poll whose stored tallies were zeroed
// while two local votes (A, C) exist. A refetch restores [1,0,1].
func TestRefetchRemotePollRepairsZeroedTallies(t *testing.T) {
	conn, remote := setupProfileBackfill(t)
	require.NoError(t, conn.AutoMigrate(&models.Poll{}, &models.PollOption{}, &models.PollOptionVote{}))
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	prev := refetchPollClient
	refetchPollClient = func(context.Context) (*Client, error) {
		c := NewClient(key, "https://local.example/actor")
		c.httpClient = ssrfHTTPClientForTesting()
		return c, nil
	}
	t.Cleanup(func() { refetchPollClient = prev })

	fixture := loadPollFixture(t, "misskey_anyof_closed.json")
	remote.docs["/notes/asf7i5c4w71800ss"] = func(b string) any {
		fixture["id"] = b + "/notes/asf7i5c4w71800ss"
		fixture["attributedTo"] = b + "/users/kanna"
		return fixture
	}
	b := remote.server.URL
	kanna := remoteAccount(t, conn, b, "kanna")
	uri := b + "/notes/asf7i5c4w71800ss"
	poll := questionPoll(fixture, kanna.Id)
	for _, o := range poll.Options {
		o.Votes = 0 // the broken state
	}
	status := &models.Status{AccountId: kanna.Id, Domain: kanna.Domain, OriginUri: &uri, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "multi"}, Poll: poll}
	require.NoError(t, conn.Create(status).Error)
	qa := &models.Account{Username: "qa"}
	require.NoError(t, conn.Create(qa).Error)
	opts := status.Poll.Options.SortByIndex()
	for _, i := range []int{0, 2} {
		require.NoError(t, conn.Create(&models.PollOptionVote{PollId: status.Poll.Id, OptionId: opts[i].Id, AccountId: qa.Id}).Error)
	}
	assert.True(t, pollLooksInconsistent(status.Poll, 2, time.Now()))

	require.NoError(t, RefetchRemotePoll(context.Background(), status.Id))
	var got models.Poll
	require.NoError(t, conn.Preload("Options").First(&got, status.Poll.Id).Error)
	votes := []uint64{}
	for _, o := range got.Options.SortByIndex() {
		votes = append(votes, o.Votes)
	}
	assert.Equal(t, []uint64{1, 0, 1}, votes)
	assert.False(t, pollLooksInconsistent(&got, 2, time.Now()))
}

func TestMaybeRepairRemotePollIsThrottledAndOnlyForInconsistentClosedPolls(t *testing.T) {
	prev := startPollRepair
	runs := 0
	startPollRepair = func(func()) { runs++ }
	t.Cleanup(func() { startPollRepair = prev })
	past := time.Now().Add(-time.Hour)
	closed := &models.Poll{StatusId: 987654, ExpiresAt: &past, Properties: map[string]any{"remote": true}, Options: models.PollOptions{{Votes: 0}, {Votes: 0}}}
	MaybeRepairRemotePoll(closed, 2)
	MaybeRepairRemotePoll(closed, 2)
	assert.Equal(t, 1, runs, "one repair per status per hour")

	future := time.Now().Add(time.Hour)
	open := &models.Poll{StatusId: 987655, ExpiresAt: &future, Properties: map[string]any{"remote": true}}
	MaybeRepairRemotePoll(open, 2)
	local := &models.Poll{StatusId: 987656, ExpiresAt: &past, Options: models.PollOptions{{Votes: 0}}}
	MaybeRepairRemotePoll(local, 2)
	consistent := &models.Poll{StatusId: 987657, ExpiresAt: &past, Properties: map[string]any{"remote": true}, Options: models.PollOptions{{Votes: 2}}}
	MaybeRepairRemotePoll(consistent, 2)
	assert.Equal(t, 1, runs)
}
