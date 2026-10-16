package activitypub

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadPollFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile("testdata/poll/" + name)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func TestQuestionPollParsesMastodonQuestion(t *testing.T) {
	poll := questionPoll(loadPollFixture(t, "mastodon_question.json"), 5)
	require.NotNil(t, poll)
	assert.False(t, poll.Multiple)
	assert.Equal(t, uint64(5), poll.AccountId)
	require.Len(t, poll.Options, 2)
	assert.Equal(t, "Tea", poll.Options[0].Content)
	assert.Equal(t, uint64(4), poll.Options[0].Votes)
	assert.Equal(t, "Coffee", poll.Options[1].Content)
	assert.Equal(t, uint64(3), poll.Options[1].Votes)
	require.NotNil(t, poll.ExpiresAt)
	assert.Equal(t, "2026-10-17T10:00:00Z", poll.ExpiresAt.UTC().Format(time.RFC3339))
	assert.Equal(t, uint64(7), poll.RemoteVotersCount())
	assert.False(t, poll.IsClosed(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)))
	assert.True(t, poll.IsClosed(time.Date(2026, 10, 17, 10, 0, 1, 0, time.UTC)))
}

func TestQuestionPollParsesClosedQuestion(t *testing.T) {
	poll := questionPoll(loadPollFixture(t, "mastodon_question_closed.json"), 5)
	require.NotNil(t, poll)
	assert.Equal(t, true, poll.Properties["closed"])
	// closed earlier than endTime: the poll ended at the closing time.
	assert.Equal(t, "2026-10-16T12:00:00Z", poll.ExpiresAt.UTC().Format(time.RFC3339))
	assert.True(t, poll.IsClosed(time.Date(2026, 10, 16, 13, 0, 0, 0, time.UTC)))
}

func TestQuestionPollParsesMisskeyQuestion(t *testing.T) {
	poll := questionPoll(loadPollFixture(t, "misskey_question.json"), 9)
	require.NotNil(t, poll)
	assert.True(t, poll.Multiple)
	assert.Equal(t, []string{"选项A", "选项B", "选项C"}, poll.Options.GetNames())
	assert.Equal(t, uint64(2), poll.Options[0].Votes)
	assert.Equal(t, "2026-10-18T10:00:00Z", poll.ExpiresAt.UTC().Format(time.RFC3339))
}

func TestQuestionPollIgnoresNotes(t *testing.T) {
	assert.Nil(t, questionPoll(map[string]any{"type": "Note", "oneOf": []any{map[string]any{"name": "x"}}}, 1))
	assert.Nil(t, questionPoll(map[string]any{"type": "Question"}, 1))
}

func TestIsPollVoteNote(t *testing.T) {
	vote := loadPollFixture(t, "mastodon_vote.json")["object"].(map[string]any)
	assert.True(t, isPollVoteNote(vote))
	assert.False(t, isPollVoteNote(map[string]any{"type": "Note", "inReplyTo": "https://x/1", "content": "<p>a reply</p>"}))
	assert.False(t, isPollVoteNote(map[string]any{"type": "Note", "name": "Yes"}))
}

// The serialized Question must have the exact shape Mastodon emits.
func TestApplyQuestionFieldsMatchesMastodonShape(t *testing.T) {
	end := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	poll := &models.Poll{ExpiresAt: &end, Options: models.PollOptions{
		{Index: 1, Content: "Coffee", Votes: 3},
		{Index: 0, Content: "Tea", Votes: 4},
	}, Properties: map[string]any{"voters_count": float64(7)}}
	note := map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "type": "Note"}
	applyQuestionFields(note, poll, end.Add(-time.Hour))

	fixture := loadPollFixture(t, "mastodon_question.json")
	got, err := json.Marshal(map[string]any{"type": note["type"], "oneOf": note["oneOf"], "endTime": note["endTime"], "votersCount": note["votersCount"]})
	require.NoError(t, err)
	want, err := json.Marshal(map[string]any{"type": fixture["type"], "oneOf": fixture["oneOf"], "endTime": fixture["endTime"], "votersCount": fixture["votersCount"]})
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
	assert.NotContains(t, note, "closed")
	assert.NotContains(t, note, "anyOf")
	ctx, _ := json.Marshal(note["@context"])
	assert.Contains(t, string(ctx), `"votersCount":"toot:votersCount"`)
	assert.Contains(t, string(ctx), `"toot":"http://joinmastodon.org/ns#"`)

	// Ended multiple-choice poll: anyOf + closed.
	poll.Multiple = true
	note = map[string]any{"@context": emojiContext(), "type": "Note"}
	applyQuestionFields(note, poll, end.Add(time.Minute))
	assert.Equal(t, "2026-10-17T10:00:00Z", note["closed"])
	assert.Len(t, note["anyOf"], 2)
	ctx, _ = json.Marshal(note["@context"])
	assert.Contains(t, string(ctx), `"Emoji":"toot:Emoji"`)
	assert.Contains(t, string(ctx), `"votersCount":"toot:votersCount"`)
}

func setupPollDB(t *testing.T) *testutil.TestDB {
	t.Helper()
	testDB := testutil.QuickDBSetup(t,
		&models.Account{}, &models.AccountExtra{}, &models.AccountKey{}, &models.Follow{}, &models.Block{},
		&models.Status{}, &models.StatusContent{}, &models.StatusToAccount{}, &models.Tag{}, &models.StatusTag{},
		&models.MediaAttachment{}, &models.Emoji{}, &models.StatusEmoji{}, &models.Application{},
		&models.Conversation{}, &models.AccountConversation{}, &models.SystemSetting{},
		&models.Poll{}, &models.PollOption{}, &models.PollOptionVote{},
		&models.Notification{}, &models.FederationOutbox{},
	)
	database.SetTestDB(testDB.GormDB)
	t.Cleanup(database.ResetTestDB)
	return testDB
}

func createLocalPoll(t *testing.T, testDB *testutil.TestDB, expires time.Time, multiple bool) (*models.Account, *models.Status) {
	t.Helper()
	author := &models.Account{Username: "luna"}
	require.NoError(t, testDB.GormDB.Create(author).Error)
	status := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "测试发起投票"},
		Poll: &models.Poll{AccountId: author.Id, ExpiresAt: &expires, Multiple: multiple, Options: models.PollOptions{{Index: 0, Content: "Yes"}, {Index: 1, Content: "No"}}}}
	require.NoError(t, testDB.GormDB.Create(status).Error)
	status.Account = author
	return author, status
}

func pollRemoteAccount(t *testing.T, testDB *testutil.TestDB, name string) *models.Account {
	t.Helper()
	domain := "mastodon.example"
	acct := &models.Account{Username: name, Domain: &domain, OriginId: "https://mastodon.example/users/" + name}
	require.NoError(t, testDB.GormDB.Create(acct).Error)
	return acct
}

func TestLocalPollIsServedAsQuestion(t *testing.T) {
	testDB := setupPollDB(t)
	_, status := createLocalPoll(t, testDB, time.Now().Add(time.Hour), false)
	status.Poll = nil // the object endpoint does not preload polls
	note, err := StatusToNote(status)
	require.NoError(t, err)
	assert.Equal(t, "Question", note["type"])
	options, ok := note["oneOf"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, options, 2)
	assert.Equal(t, "Yes", options[0]["name"])
	assert.Equal(t, map[string]any{"type": "Collection", "totalItems": uint64(0)}, options[0]["replies"])
	assert.NotEmpty(t, note["endTime"])
	assert.Equal(t, uint64(0), note["votersCount"])
}

func TestRemoteVoteIsCountedOnceAndQueuesUpdate(t *testing.T) {
	testDB := setupPollDB(t)
	author, status := createLocalPoll(t, testDB, time.Now().Add(time.Hour), false)
	bob := pollRemoteAccount(t, testDB, "bob")
	pollURI := author.ActorURI(systemsettings.GetBaseURL()) + "/statuses/" + strconv.FormatUint(status.Id, 10)
	raw, _ := os.ReadFile("testdata/poll/mastodon_vote.json")
	var activity map[string]any
	require.NoError(t, json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "POLL_URI", pollURI)), &activity))
	object := activity["object"].(map[string]any)

	require.NoError(t, recordRemoteVote(context.Background(), bob, object))
	require.NoError(t, recordRemoteVote(context.Background(), bob, object)) // duplicate delivery
	object["name"] = "No"
	require.NoError(t, recordRemoteVote(context.Background(), bob, object)) // single choice: second option rejected

	var options []models.PollOption
	require.NoError(t, testDB.GormDB.Order("\"index\" ASC").Find(&options).Error)
	assert.Equal(t, uint64(1), options[0].Votes)
	assert.Equal(t, uint64(0), options[1].Votes)
	var votes int64
	testDB.GormDB.Model(&models.PollOptionVote{}).Count(&votes)
	assert.Equal(t, int64(1), votes)
	var updates []models.FederationOutbox
	require.NoError(t, testDB.GormDB.Where("kind = ?", "Update").Find(&updates).Error)
	require.Len(t, updates, 1, "pending Updates are coalesced")
	assert.Contains(t, updates[0].RecipientIDs, bob.Id, "remote voters receive tally updates")

	other := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "plain"}}
	require.NoError(t, testDB.GormDB.Create(other).Error)
	object["inReplyTo"] = author.ActorURI(systemsettings.GetBaseURL()) + "/statuses/" + strconv.FormatUint(other.Id, 10)
	assert.ErrorIs(t, recordRemoteVote(context.Background(), bob, object), errNotLocalPoll)
}

func TestClosedPollNotifiesOnceAndSendsClosingUpdate(t *testing.T) {
	testDB := setupPollDB(t)
	author, status := createLocalPoll(t, testDB, time.Now().Add(-time.Minute), false)
	voter := &models.Account{Username: "voter"}
	require.NoError(t, testDB.GormDB.Create(voter).Error)
	bob := pollRemoteAccount(t, testDB, "bob")
	opt := status.Poll.Options[0]
	for _, id := range []uint64{voter.Id, bob.Id} {
		require.NoError(t, testDB.GormDB.Create(&models.PollOptionVote{PollId: status.Poll.Id, OptionId: opt.Id, AccountId: id}).Error)
	}

	require.NoError(t, ProcessClosedPolls(context.Background(), time.Now(), 10))
	require.NoError(t, ProcessClosedPolls(context.Background(), time.Now(), 10))

	var nfs []models.Notification
	require.NoError(t, testDB.GormDB.Where("type = ?", models.NotificationTypePoll).Find(&nfs).Error)
	got := map[uint64]int{}
	for _, n := range nfs {
		got[n.AccountId]++
	}
	assert.Equal(t, map[uint64]int{author.Id: 1, voter.Id: 1}, got, "author and local voters only, once")
	assert.Equal(t, "poll", (&models.Notification{Type: models.NotificationTypePoll}).GetTypeString())

	var updates []models.FederationOutbox
	require.NoError(t, testDB.GormDB.Where("kind = ?", "Update").Find(&updates).Error)
	require.Len(t, updates, 1)
	assert.Contains(t, updates[0].RecipientIDs, bob.Id)
}

func TestRemotePollUpdateAppliesTalliesAndClosure(t *testing.T) {
	testDB := setupPollDB(t)
	alice := pollRemoteAccount(t, testDB, "alice")
	open := loadPollFixture(t, "mastodon_question.json")
	status := &models.Status{AccountId: alice.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "Tea or coffee?"}, Poll: questionPoll(open, alice.Id)}
	require.NoError(t, testDB.GormDB.Create(status).Error)
	voter := &models.Account{Username: "voter"}
	require.NoError(t, testDB.GormDB.Create(voter).Error)
	require.NoError(t, testDB.GormDB.Create(&models.PollOptionVote{PollId: status.Poll.Id, OptionId: status.Poll.Options[0].Id, AccountId: voter.Id}).Error)

	require.NoError(t, updateRemotePoll(testDB.GormDB, status.Id, alice.Id, loadPollFixture(t, "mastodon_question_closed.json")))
	var poll models.Poll
	require.NoError(t, testDB.GormDB.Preload("Options").First(&poll, status.Poll.Id).Error)
	assert.Equal(t, uint64(6), poll.Options.SortByIndex()[0].Votes)
	assert.Equal(t, uint64(9), poll.RemoteVotersCount())
	assert.True(t, poll.IsClosed(time.Now()))

	require.NoError(t, ProcessClosedPolls(context.Background(), time.Now(), 10))
	require.NoError(t, ProcessClosedPolls(context.Background(), time.Now(), 10))
	var nfs []models.Notification
	require.NoError(t, testDB.GormDB.Where("type = ?", models.NotificationTypePoll).Find(&nfs).Error)
	require.Len(t, nfs, 1, "only the local voter of a remote poll is notified")
	assert.Equal(t, voter.Id, nfs[0].AccountId)
	var outbox int64
	testDB.GormDB.Model(&models.FederationOutbox{}).Count(&outbox)
	assert.Zero(t, outbox, "remote polls are never re-federated")
}

func TestPollVoteActivitiesAreMastodonVotes(t *testing.T) {
	domain := "mastodon.example"
	author := &models.Account{Id: 2, Username: "alice", Domain: &domain, OriginId: "https://mastodon.example/users/alice"}
	uri := "https://mastodon.example/users/alice/statuses/1"
	status := &models.Status{Id: 10, AccountId: 2, Account: author, OriginUri: &uri}
	voter := &models.Account{Id: 1, Username: "luna"}
	acts := pollVoteActivities(voter, status, []string{"Tea", "Coffee"}, "https://c.example", "https://c.example/users/luna/activities/5")
	require.Len(t, acts, 2)
	obj := acts[0]["object"].(map[string]any)
	assert.Equal(t, "Create", acts[0]["type"])
	assert.Equal(t, "Note", obj["type"])
	assert.Equal(t, "Tea", obj["name"])
	assert.Equal(t, uri, obj["inReplyTo"])
	assert.Equal(t, []string{author.OriginId}, obj["to"])
	assert.NotEqual(t, acts[0]["id"], acts[1]["id"])
}
