package search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/meilisearch/meilisearch-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMeili is a minimal Meilisearch HTTP server: document additions and
// batch deletes are recorded and acknowledged with a task uid, and
// GET /tasks reports each task as succeeded unless told otherwise.
type fakeMeili struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	nextUID     int64
	upserts     []fakeWrite
	deletes     []fakeWrite
	failPosts   int            // answer this many document POSTs with HTTP 500
	failTask    map[int64]bool // task uids GET /tasks reports as failed
	failFirstN  int            // report the first N tasks as failed
	taskGets    int            // GET /tasks list requests
	taskUIDGets map[int64]int  // GET /tasks/{uid} requests
	doneAfter   map[int64]time.Time
}

type fakeWrite struct {
	index string
	task  int64
	docs  []map[string]any // upserts
	ids   []string         // deletes
}

func newFakeMeili(t *testing.T) *fakeMeili {
	f := &fakeMeili{t: t, failTask: map[int64]bool{}, taskUIDGets: map[int64]int{}, doneAfter: map[int64]time.Time{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMeili) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/indexes/") && (strings.HasSuffix(path, "/documents") || strings.HasSuffix(path, "/documents/delete-batch")):
		if f.failPosts > 0 {
			f.failPosts--
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"boom","code":"internal","type":"internal"}`)
			return
		}
		index := strings.Split(strings.TrimPrefix(path, "/indexes/"), "/")[0]
		body, _ := io.ReadAll(r.Body)
		f.nextUID++
		write := fakeWrite{index: index, task: f.nextUID}
		if strings.HasSuffix(path, "/delete-batch") {
			require.NoError(f.t, json.Unmarshal(body, &write.ids))
			f.deletes = append(f.deletes, write)
		} else {
			require.NoError(f.t, json.Unmarshal(body, &write.docs), string(body))
			f.upserts = append(f.upserts, write)
		}
		if f.failFirstN > 0 {
			f.failFirstN--
			f.failTask[write.task] = true
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = fmt.Fprintf(w, `{"taskUid":%d,"indexUid":%q,"status":"enqueued","type":"documentAdditionOrUpdate","enqueuedAt":"2026-10-19T00:00:00Z"}`, write.task, index)
	case r.Method == http.MethodGet && path == "/tasks":
		f.taskGets++
		var results []string
		for _, raw := range strings.Split(r.URL.Query().Get("uids"), ",") {
			uid, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				continue
			}
			results = append(results, f.taskJSON(uid))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"results":[%s],"limit":20,"from":0,"next":0,"total":%d}`, strings.Join(results, ","), len(results))
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/tasks/"):
		uid, _ := strconv.ParseInt(strings.TrimPrefix(path, "/tasks/"), 10, 64)
		f.taskUIDGets[uid]++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, f.taskJSON(uid))
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeMeili) taskJSON(uid int64) string {
	status, errJSON := "succeeded", ""
	if at, ok := f.doneAfter[uid]; ok && time.Now().Before(at) {
		status = "processing"
	}
	if f.failTask[uid] {
		status, errJSON = "failed", `,"error":{"message":"invalid document id","code":"invalid_document_id","type":"invalid_request"}`
	}
	return fmt.Sprintf(`{"uid":%d,"indexUid":"statuses","status":%q,"type":"documentAdditionOrUpdate","enqueuedAt":"2026-10-19T00:00:00Z"%s}`, uid, status, errJSON)
}

func (f *fakeMeili) snapshot() (ups, dels []fakeWrite) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeWrite(nil), f.upserts...), append([]fakeWrite(nil), f.deletes...)
}

func (f *fakeMeili) client() *MeilisearchClient {
	c := &MeilisearchClient{host: f.srv.URL}
	c.ensureClient()
	c.setEnabled(true)
	return c
}

// fastConfig never flushes on its own unless a test lowers FlushInterval.
func fastConfig() IndexQueueConfig {
	return IndexQueueConfig{
		FlushInterval:     time.Hour,
		MaxDocs:           500,
		MaxBytes:          5 << 20,
		MaxRetries:        5,
		TaskCheckInterval: time.Hour,
		BaseBackoff:       10 * time.Millisecond,
		MaxBackoff:        50 * time.Millisecond,
	}
}

func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", msg)
}

func statusDoc(id, content string) *StatusSearchDocument {
	return &StatusSearchDocument{ID: id, Content: content, AccountID: 1, Visibility: 0}
}

func TestIndexQueue_UpsertThenDeleteSendsOnlyDelete(t *testing.T) {
	f := newFakeMeili(t)
	q := newIndexQueue(f.client(), fastConfig())
	defer q.Close(context.Background())

	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "hello")))
	require.NoError(t, q.Delete(IndexStatuses, "1"))
	require.NoError(t, q.Flush(context.Background()))

	ups, dels := f.snapshot()
	assert.Empty(t, ups, "the upsert was superseded by the delete")
	require.Len(t, dels, 1)
	assert.Equal(t, []string{"1"}, dels[0].ids)
	assert.Equal(t, "statuses", dels[0].index)
}

func TestIndexQueue_DeleteThenUpsertSendsOnlyUpsert(t *testing.T) {
	f := newFakeMeili(t)
	q := newIndexQueue(f.client(), fastConfig())
	defer q.Close(context.Background())

	require.NoError(t, q.Delete(IndexStatuses, "1"))
	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "back")))
	require.NoError(t, q.Flush(context.Background()))

	ups, dels := f.snapshot()
	assert.Empty(t, dels)
	require.Len(t, ups, 1)
	assert.Equal(t, "back", ups[0].docs[0]["content"])
}

func TestIndexQueue_MultipleUpdatesBecomeOneDocument(t *testing.T) {
	f := newFakeMeili(t)
	q := newIndexQueue(f.client(), fastConfig())
	defer q.Close(context.Background())

	for i := 1; i <= 5; i++ {
		require.NoError(t, q.Upsert(IndexStatuses, "7", statusDoc("7", fmt.Sprintf("v%d", i))))
	}
	require.NoError(t, q.Upsert(IndexStatuses, "8", statusDoc("8", "other")))
	require.NoError(t, q.Upsert(IndexAccounts, "7", &AccountSearchDocument{ID: "7", Username: "alice"}))
	require.NoError(t, q.Flush(context.Background()))

	ups, dels := f.snapshot()
	assert.Empty(t, dels)
	require.Len(t, ups, 2, "one request per index")
	byIndex := map[string]fakeWrite{}
	for _, u := range ups {
		byIndex[u.index] = u
	}
	st := byIndex["statuses"]
	require.Len(t, st.docs, 2)
	contents := map[string]any{}
	for _, d := range st.docs {
		contents[d["id"].(string)] = d["content"]
	}
	assert.Equal(t, map[string]any{"7": "v5", "8": "other"}, contents)
	require.Len(t, byIndex["accounts"].docs, 1)
	assert.Equal(t, uint64(4), q.Stats().Coalesced)
}

func TestIndexQueue_FlushesWhenBatchReachesDocLimit(t *testing.T) {
	f := newFakeMeili(t)
	cfg := fastConfig()
	cfg.MaxDocs = 3
	q := newIndexQueue(f.client(), cfg)
	defer q.Close(context.Background())

	for i := 1; i <= 3; i++ {
		require.NoError(t, q.Upsert(IndexStatuses, strconv.Itoa(i), statusDoc(strconv.Itoa(i), "x")))
	}
	eventually(t, func() bool { ups, _ := f.snapshot(); return len(ups) == 1 }, "size-triggered flush")
	ups, _ := f.snapshot()
	assert.Len(t, ups[0].docs, 3)
}

func TestIndexQueue_FlushesWhenBatchReachesByteLimit(t *testing.T) {
	f := newFakeMeili(t)
	cfg := fastConfig()
	cfg.MaxBytes = 1000
	q := newIndexQueue(f.client(), cfg)
	defer q.Close(context.Background())

	big := strings.Repeat("a", 600)
	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", big)))
	time.Sleep(50 * time.Millisecond)
	ups, _ := f.snapshot()
	assert.Empty(t, ups, "below the byte limit nothing is sent before the timer")
	require.NoError(t, q.Upsert(IndexStatuses, "2", statusDoc("2", big)))
	eventually(t, func() bool { ups, _ := f.snapshot(); return len(ups) >= 1 }, "byte-triggered flush")
	// Each request stays under the byte limit, so the two docs go separately.
	eventually(t, func() bool { ups, _ := f.snapshot(); return len(ups) == 2 }, "split by bytes")
	ups, _ = f.snapshot()
	assert.Len(t, ups[0].docs, 1)
	assert.Len(t, ups[1].docs, 1)
}

func TestIndexQueue_FlushesOnTimer(t *testing.T) {
	f := newFakeMeili(t)
	cfg := fastConfig()
	cfg.FlushInterval = 50 * time.Millisecond
	q := newIndexQueue(f.client(), cfg)
	defer q.Close(context.Background())

	start := time.Now()
	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "tick")))
	require.NoError(t, q.Upsert(IndexHashtags, "go", &HashtagSearchDocument{ID: "go", Name: "go"}))
	eventually(t, func() bool { ups, _ := f.snapshot(); return len(ups) == 2 }, "timer flush")
	assert.GreaterOrEqual(t, time.Since(start), 40*time.Millisecond, "not sent before the interval")
}

func TestIndexQueue_RetriesWhenRequestFails(t *testing.T) {
	f := newFakeMeili(t)
	f.failPosts = 2
	cfg := fastConfig()
	cfg.FlushInterval = 20 * time.Millisecond
	q := newIndexQueue(f.client(), cfg)
	defer q.Close(context.Background())

	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "retry me")))
	eventually(t, func() bool { ups, _ := f.snapshot(); return len(ups) == 1 }, "retried after HTTP 500")
	ups, _ := f.snapshot()
	assert.Equal(t, "retry me", ups[0].docs[0]["content"])
	st := q.Stats()
	assert.Equal(t, uint64(2), st.SendErrors)
	assert.Equal(t, uint64(2), st.Retried)
	assert.Zero(t, st.GaveUp)
}

func TestIndexQueue_RetriesFailedTaskInHalves(t *testing.T) {
	f := newFakeMeili(t)
	f.failFirstN = 1 // the first batch's task fails asynchronously
	cfg := fastConfig()
	cfg.FlushInterval = 20 * time.Millisecond
	cfg.TaskCheckInterval = 20 * time.Millisecond
	q := newIndexQueue(f.client(), cfg)
	defer q.Close(context.Background())

	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "a")))
	require.NoError(t, q.Upsert(IndexStatuses, "2", statusDoc("2", "b")))
	eventually(t, func() bool { ups, _ := f.snapshot(); return len(ups) == 3 }, "failed batch resent as two halves")
	ups, _ := f.snapshot()
	assert.Len(t, ups[0].docs, 2)
	assert.Len(t, ups[1].docs, 1)
	assert.Len(t, ups[2].docs, 1)
	eventually(t, func() bool { return q.Stats().Inflight == 0 }, "retried tasks confirmed")
	assert.Equal(t, uint64(1), q.Stats().TaskFailures)
}

func TestIndexQueue_RetryNeverOverwritesNewerDocument(t *testing.T) {
	f := newFakeMeili(t)
	f.failFirstN = 1
	cfg := fastConfig()
	cfg.TaskCheckInterval = time.Millisecond
	q := newIndexQueue(f.client(), cfg)
	defer q.Close(context.Background())

	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "old")))
	require.NoError(t, q.Flush(context.Background())) // task 1 will fail
	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "new")))
	require.NoError(t, q.Flush(context.Background())) // sends "new"
	time.Sleep(5 * time.Millisecond)
	q.runOnce(false) // task check: task 1 failed -> retry scheduled
	time.Sleep(60 * time.Millisecond)
	q.runOnce(false) // retry is due but superseded by "new"
	ups, _ := f.snapshot()
	require.Len(t, ups, 2)
	assert.Equal(t, "old", ups[0].docs[0]["content"])
	assert.Equal(t, "new", ups[1].docs[0]["content"])
}

func TestIndexQueue_GivesUpAfterMaxRetries(t *testing.T) {
	f := newFakeMeili(t)
	f.failPosts = 100
	cfg := fastConfig()
	cfg.MaxRetries = 2
	cfg.FlushInterval = 10 * time.Millisecond
	q := newIndexQueue(f.client(), cfg)
	defer q.Close(context.Background())

	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "x")))
	eventually(t, func() bool { return q.Stats().GaveUp == 1 }, "dropped after retries")
	st := q.Stats()
	assert.Equal(t, uint64(3), st.SendErrors)
	assert.Zero(t, st.Pending)
	assert.Zero(t, st.RetryBatches)
}

func TestIndexQueue_CloseFlushesPendingWrites(t *testing.T) {
	f := newFakeMeili(t)
	q := newIndexQueue(f.client(), fastConfig()) // hour-long interval
	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "bye")))
	require.NoError(t, q.Delete(IndexStatuses, "2"))

	require.NoError(t, q.Close(context.Background()))
	ups, dels := f.snapshot()
	require.Len(t, ups, 1)
	require.Len(t, dels, 1)

	// After Close nothing new is accepted.
	require.NoError(t, q.Upsert(IndexStatuses, "3", statusDoc("3", "late")))
	assert.Equal(t, 0, q.Stats().Pending)
	require.NoError(t, q.Close(context.Background()), "Close is idempotent")
}

func TestIndexQueue_CloseIgnoresBackoff(t *testing.T) {
	f := newFakeMeili(t)
	f.failPosts = 1
	cfg := fastConfig()
	cfg.BaseBackoff = time.Hour
	cfg.MaxBackoff = time.Hour
	q := newIndexQueue(f.client(), cfg)
	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "x")))
	q.runOnce(false) // fails, backs off for an hour
	require.Equal(t, 1, q.Stats().RetryBatches)
	require.NoError(t, q.Close(context.Background()))
	ups, _ := f.snapshot()
	require.Len(t, ups, 1)
}

func TestIndexQueue_HoldsWritesWhileEngineOffline(t *testing.T) {
	f := newFakeMeili(t)
	c := f.client()
	q := newIndexQueue(c, fastConfig())
	defer q.Close(context.Background())

	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "x")))
	c.setEnabled(false)
	assert.Error(t, q.Flush(context.Background()))
	ups, _ := f.snapshot()
	assert.Empty(t, ups)
	c.setEnabled(true)
	require.NoError(t, q.Flush(context.Background()))
	ups, _ = f.snapshot()
	assert.Len(t, ups, 1)
}

func TestIndexQueue_BoundedQueueDropsNewDocuments(t *testing.T) {
	f := newFakeMeili(t)
	cfg := fastConfig()
	cfg.MaxPending = 2
	q := newIndexQueue(f.client(), cfg)
	defer q.Close(context.Background())
	for i := 1; i <= 4; i++ {
		require.NoError(t, q.Upsert(IndexStatuses, strconv.Itoa(i), statusDoc(strconv.Itoa(i), "x")))
	}
	// Replacing an already queued document is still allowed.
	require.NoError(t, q.Upsert(IndexStatuses, "1", statusDoc("1", "y")))
	st := q.Stats()
	assert.Equal(t, 2, st.Pending)
	assert.Equal(t, uint64(2), st.Dropped)
}

func TestIndexQueue_TaskChecksAreRateLimited(t *testing.T) {
	f := newFakeMeili(t)
	cfg := fastConfig()
	cfg.FlushInterval = 5 * time.Millisecond
	cfg.TaskCheckInterval = 200 * time.Millisecond
	q := newIndexQueue(f.client(), cfg)
	defer q.Close(context.Background())

	start := time.Now()
	for i := 0; i < 50; i++ {
		require.NoError(t, q.Upsert(IndexStatuses, strconv.Itoa(i), statusDoc(strconv.Itoa(i), "x")))
		time.Sleep(10 * time.Millisecond)
	}
	elapsed := time.Since(start)
	f.mu.Lock()
	gets, perTask := f.taskGets, len(f.taskUIDGets)
	f.mu.Unlock()
	assert.Zero(t, perTask, "no per-task polling")
	assert.LessOrEqual(t, gets, int(elapsed/cfg.TaskCheckInterval)+1, "one GET /tasks per check interval at most")
	assert.GreaterOrEqual(t, gets, 1)
}

func TestIndexQueue_EnvConfig(t *testing.T) {
	t.Setenv("SEARCH_INDEX_FLUSH_INTERVAL_MS", "2000")
	t.Setenv("SEARCH_INDEX_BATCH_MAX_DOCS", "250")
	t.Setenv("SEARCH_INDEX_BATCH_MAX_BYTES", "1048576")
	t.Setenv("SEARCH_INDEX_TASK_CHECK_INTERVAL_MS", "10") // clamped to 1s
	cfg := IndexQueueConfigFromEnv()
	assert.Equal(t, 2*time.Second, cfg.FlushInterval)
	assert.Equal(t, 250, cfg.MaxDocs)
	assert.Equal(t, 1<<20, cfg.MaxBytes)
	assert.Equal(t, time.Second, cfg.TaskCheckInterval)

	t.Setenv("SEARCH_INDEX_FLUSH_INTERVAL_MS", "")
	t.Setenv("SEARCH_INDEX_BATCH_MAX_DOCS", "")
	t.Setenv("SEARCH_INDEX_BATCH_MAX_BYTES", "")
	t.Setenv("SEARCH_INDEX_TASK_CHECK_INTERVAL_MS", "")
	cfg = IndexQueueConfigFromEnv()
	assert.Equal(t, 3*time.Second, cfg.FlushInterval)
	assert.Equal(t, 500, cfg.MaxDocs)
	assert.Equal(t, 5<<20, cfg.MaxBytes)
	assert.Equal(t, 5*time.Second, cfg.TaskCheckInterval)
}

func TestClientQueueMethods(t *testing.T) {
	f := newFakeMeili(t)
	c := f.client()
	require.NoError(t, c.QueueStatus(statusDoc("1", "x")))
	require.NoError(t, c.QueueAccount(&AccountSearchDocument{ID: "2", Username: "bob"}))
	require.NoError(t, c.QueueHashtag(&HashtagSearchDocument{ID: "go", Name: "go"}))
	require.NoError(t, c.QueueHashtag(&HashtagSearchDocument{ID: "bad id", Name: "<b>"}), "invalid tags are skipped")
	require.NoError(t, c.QueueDelete(IndexStatuses, "3"))
	assert.Error(t, c.QueueStatus(&StatusSearchDocument{}))
	assert.Equal(t, 4, c.IndexQueue().Stats().Pending)
	require.NoError(t, c.IndexQueue().Close(context.Background()))
	ups, dels := f.snapshot()
	assert.Len(t, ups, 3)
	assert.Len(t, dels, 1)

	off := &MeilisearchClient{}
	require.NoError(t, off.QueueStatus(statusDoc("1", "x")), "disabled engine: no-op")
}

func TestFinishTaskPollsWithBackoff(t *testing.T) {
	f := newFakeMeili(t)
	f.doneAfter[42] = time.Now().Add(1500 * time.Millisecond)
	c := f.client()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, finishTask(ctx, c.client, &meilisearch.TaskInfo{TaskUID: 42}))
	f.mu.Lock()
	n := f.taskUIDGets[42]
	f.mu.Unlock()
	// 100+200+400+800 ms covers 1.5s: about 5 polls, not 30 at 50ms.
	assert.LessOrEqual(t, n, 6)
	assert.GreaterOrEqual(t, n, 3)
}

func TestIndexQueue_ForgetsConfirmedDocuments(t *testing.T) {
	f := newFakeMeili(t)
	cfg := fastConfig()
	cfg.TaskCheckInterval = time.Millisecond
	q := newIndexQueue(f.client(), cfg)
	defer q.Close(context.Background())
	for i := 0; i < 10; i++ {
		require.NoError(t, q.Upsert(IndexStatuses, strconv.Itoa(i), statusDoc(strconv.Itoa(i), "x")))
	}
	require.NoError(t, q.Delete(IndexStatuses, "3"))
	require.NoError(t, q.Flush(context.Background()))
	time.Sleep(5 * time.Millisecond)
	q.runOnce(false) // one GET /tasks confirms both batches
	q.mu.Lock()
	defer q.mu.Unlock()
	assert.Empty(t, q.live)
	assert.Empty(t, q.inflight)
	f.mu.Lock()
	assert.Equal(t, 1, f.taskGets)
	f.mu.Unlock()
}
