package activitypub

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// withTestInboxQueue installs a queue without goroutines; tests drive it
// with processDueInboxJobs.
func withTestInboxQueue(t *testing.T) {
	q := &InboxQueue{workers: 1, wakeups: []chan struct{}{make(chan struct{}, 1)}, stop: make(chan struct{})}
	activeInboxQueue.Store(q)
	t.Cleanup(func() { activeInboxQueue.CompareAndSwap(q, nil) })
}

func queuedJobs(t *testing.T, w *relayWorld) []InboxJob {
	var jobs []InboxJob
	require.NoError(t, w.conn.Order("id").Find(&jobs).Error)
	return jobs
}

func queuedAliceCreate(n, content string) map[string]any {
	note := originNote(n, content)
	return map[string]any{"id": note["id"].(string) + "/activity", "type": "Create", "actor": e2eOrigin + "/users/alice",
		"to": []string{asPublicURI}, "object": note}
}

func TestInboxQueuesVerifiedActivityAndWorkerAppliesIt(t *testing.T) {
	w := newRelayWorld(t)
	withTestInboxQueue(t)
	alice := NewClient(e2eOrigKey, e2eOrigin+"/users/alice")
	create := queuedAliceCreate("1", "queued hello")

	require.Equal(t, 200, w.post(t, alice, e2eBase+"/inbox", create), "2xx (202) without processing")
	jobs := queuedJobs(t, w)
	require.Len(t, jobs, 1)
	require.Equal(t, "Create", jobs[0].ActivityType)
	require.Equal(t, e2eOrigin+"/users/alice", jobs[0].Actor)
	var n int64
	require.NoError(t, w.conn.Model(&models.Status{}).Count(&n).Error)
	require.Zero(t, n, "nothing applied on the request path")

	// Redelivery while queued does not add a second job.
	require.Equal(t, 200, w.post(t, alice, e2eBase+"/inbox", create))
	require.Len(t, queuedJobs(t, w), 1)

	require.Equal(t, 1, processDueInboxJobs(context.Background(), 1, 0, time.Now()))
	require.Empty(t, queuedJobs(t, w))
	var status models.Status
	require.NoError(t, w.conn.Where("origin_uri = ?", e2eOrigin+"/users/alice/statuses/1").First(&status).Error)

	// Already applied: acknowledged without a job.
	require.Equal(t, 200, w.post(t, alice, e2eBase+"/inbox", create))
	require.Empty(t, queuedJobs(t, w))
}

func TestInboxQueueRejectsBadSignaturesSynchronously(t *testing.T) {
	w := newRelayWorld(t)
	withTestInboxQueue(t)
	mallory := NewClient(e2eRelayKey, e2eOrigin+"/users/alice") // wrong key
	require.Equal(t, 401, w.post(t, mallory, e2eBase+"/inbox", queuedAliceCreate("2", "forged")))
	require.Empty(t, queuedJobs(t, w))
}

// Jobs of one actor are applied in arrival order: a Delete queued after
// its Create removes the post.
func TestInboxQueueKeepsActorOrder(t *testing.T) {
	w := newRelayWorld(t)
	withTestInboxQueue(t)
	alice := NewClient(e2eOrigKey, e2eOrigin+"/users/alice")
	noteID := e2eOrigin + "/users/alice/statuses/3"
	require.Equal(t, 200, w.post(t, alice, e2eBase+"/inbox", queuedAliceCreate("3", "short-lived")))
	w.origin.notes["/users/alice/statuses/3"] = nil // 410 once deleted
	require.Equal(t, 200, w.post(t, alice, e2eBase+"/inbox", map[string]any{
		"id": noteID + "#delete", "type": "Delete", "actor": e2eOrigin + "/users/alice", "to": []string{asPublicURI},
		"object": map[string]any{"id": noteID, "type": "Tombstone"},
	}))
	require.Len(t, queuedJobs(t, w), 2)
	require.Equal(t, 2, processDueInboxJobs(context.Background(), 1, 0, time.Now()))
	var n int64
	require.NoError(t, w.conn.Model(&models.Status{}).Where("origin_uri = ?", noteID).Count(&n).Error)
	require.Zero(t, n)
	require.Empty(t, queuedJobs(t, w))
}

func TestInboxJobRetriesThenDeadLetters(t *testing.T) {
	w := newRelayWorld(t)
	job := InboxJob{ActivityID: "https://x.test/a", Actor: "https://x.test/u", Body: "{}", State: inboxJobProcessing, NextAttemptAt: time.Now()}
	require.NoError(t, w.conn.Create(&job).Error)
	now := time.Now()
	for i := 1; i <= len(inboxRetryBackoff); i++ {
		finishInboxJob(w.conn, &job, http.StatusServiceUnavailable, errBusyTest, now)
		var got InboxJob
		require.NoError(t, w.conn.First(&got, job.ID).Error)
		require.Equal(t, inboxJobPending, got.State)
		require.Equal(t, i, got.Attempts)
		require.WithinDuration(t, now.Add(inboxRetryBackoff[i-1]), got.NextAttemptAt, time.Second)
	}
	finishInboxJob(w.conn, &job, http.StatusInternalServerError, errBusyTest, now)
	var dead InboxJob
	require.NoError(t, w.conn.First(&dead, job.ID).Error)
	require.Equal(t, inboxJobDead, dead.State)
	require.Equal(t, 500, dead.LastStatus)

	// Permanent refusals are dropped at once.
	other := InboxJob{ActivityID: "https://x.test/b", Actor: "https://x.test/u", Body: "{}", State: inboxJobProcessing, NextAttemptAt: now}
	require.NoError(t, w.conn.Create(&other).Error)
	finishInboxJob(w.conn, &other, http.StatusForbidden, errBusyTest, now)
	require.Error(t, w.conn.First(&InboxJob{}, other.ID).Error)
}

func TestInboxJobExpiredLeaseIsReclaimed(t *testing.T) {
	w := newRelayWorld(t)
	past := time.Now().Add(-time.Minute)
	job := InboxJob{ActivityID: "https://x.test/c", Actor: "https://x.test/u", Body: `{"type":"Bogus"}`, State: inboxJobProcessing, LeaseUntil: &past, NextAttemptAt: past}
	require.NoError(t, w.conn.Create(&job).Error)
	require.Equal(t, 1, processDueInboxJobs(context.Background(), 1, 0, time.Now()))
}

var errBusyTest = &FetchError{StatusCode: 503}
