package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"github.com/meilisearch/meilisearch-go"
)

// IndexQueue batches incremental search-index writes.
//
// Status, account and tag changes used to be pushed to Meilisearch one
// document at a time, and every push then polled GET /tasks/{uid} every 50ms
// until the task finished. Each document became its own Meilisearch task (one
// LMDB commit + fsync), and the polling added ~20 requests/s per writer. On
// spinning disks that write load starved PostgreSQL's fsyncs.
//
// The queue instead keeps the latest operation per (index, document id) in
// memory: an upsert followed by a delete leaves only the delete, repeated
// updates leave only the last document. Every flush interval (or as soon as
// one index reaches the document or byte limit) it sends one addDocuments
// and/or one deleteDocuments request per index. Callers never wait for
// Meilisearch. Task results are checked at a low rate (one GET /tasks for all
// outstanding batches, at most once per SEARCH_INDEX_TASK_CHECK_INTERVAL_MS)
// only to log failures and retry them with backoff.
//
// Durability: a change is searchable a few seconds after it happened, and
// changes still in memory when the process dies are lost. Once Meilisearch
// has accepted a batch (HTTP 202) it is stored in Meili's own task queue and
// survives restarts of either side. Lost upserts are repaired by the full
// backfill search/indexsync runs whenever the engine (re)connects, which
// includes every process start; search results are always re-read from the
// database, so a document whose delete was lost no longer shows up in
// results. A graceful shutdown flushes the queue (ShutdownIndexQueue).
type IndexQueue struct {
	cfg  IndexQueueConfig
	sink indexSink

	mu           sync.Mutex
	pending      map[string]map[string]queuedOp // index -> document id -> latest op
	pendingBytes map[string]int
	pendingCount int
	seq          uint64
	// live tracks, per document, the newest sequence number and how many
	// of its ops are still pending, in flight or waiting for a retry, so a
	// retry of an older batch never overwrites newer data.
	live          map[opKey]*keyState
	inflight      []*indexBatch
	retries       []*indexBatch
	retryOps      int
	failures      int
	backoffUntil  time.Time
	lastTaskCheck time.Time
	closed        bool
	started       bool
	stats         IndexQueueStats
	dropLogAt     time.Time

	kick chan struct{}
	stop chan struct{}
	done chan struct{}
	// flushMu serializes flushes (loop, Flush, Close).
	flushMu sync.Mutex
}

// IndexQueueConfig controls batching. Zero fields fall back to defaults.
type IndexQueueConfig struct {
	FlushInterval     time.Duration // SEARCH_INDEX_FLUSH_INTERVAL_MS, default 3s
	MaxDocs           int           // SEARCH_INDEX_BATCH_MAX_DOCS, default 500
	MaxBytes          int           // SEARCH_INDEX_BATCH_MAX_BYTES, default 5 MiB
	MaxPending        int           // SEARCH_INDEX_QUEUE_MAX, default 50000 documents
	MaxRetries        int           // SEARCH_INDEX_MAX_RETRIES, default 5
	TaskCheckInterval time.Duration // SEARCH_INDEX_TASK_CHECK_INTERVAL_MS, default 5s, never below 1s
	BaseBackoff       time.Duration // first retry delay, default 1s (doubles per attempt)
	MaxBackoff        time.Duration // cap for retry backoff, default 60s
	RequestTimeout    time.Duration // per Meili request, default 15s
}

// IndexQueueStats are cumulative counters (for tests and diagnostics).
type IndexQueueStats struct {
	Enqueued      uint64 `json:"enqueued"`
	Coalesced     uint64 `json:"coalesced"`
	Dropped       uint64 `json:"dropped"`
	Requests      uint64 `json:"requests"`
	DocsSent      uint64 `json:"docs_sent"`
	SendErrors    uint64 `json:"send_errors"`
	TaskChecks    uint64 `json:"task_checks"`
	TaskFailures  uint64 `json:"task_failures"`
	Retried       uint64 `json:"retried"`
	GaveUp        uint64 `json:"gave_up"`
	Pending       int    `json:"pending"`
	Inflight      int    `json:"inflight_batches"`
	RetryBatches  int    `json:"retry_batches"`
	LastFlushDocs int    `json:"last_flush_docs"`
}

const (
	defaultFlushInterval     = 3 * time.Second
	defaultBatchMaxDocs      = 500
	defaultBatchMaxBytes     = 5 << 20
	defaultQueueMax          = 50000
	defaultMaxRetries        = 5
	defaultTaskCheckInterval = 5 * time.Second
	minTaskCheckInterval     = time.Second
	defaultMaxBackoff        = time.Minute
	// inflightTTL stops tracking a batch Meili still has not finished; it is
	// already durable in Meili's task queue.
	inflightTTL = 10 * time.Minute
	// maxInflight bounds the batches (and their payloads) kept for retries.
	maxInflight = 256
	// maxTaskUIDsPerCheck bounds one GET /tasks request.
	maxTaskUIDsPerCheck = 100
)

type opKey struct{ index, id string }

type keyState struct {
	newest uint64
	refs   int
}

type queuedOp struct {
	id  string
	doc json.RawMessage // nil means delete
	seq uint64
}

type indexBatch struct {
	index     string
	del       bool
	ops       []queuedOp
	attempts  int
	taskUID   int64
	sentAt    time.Time
	notBefore time.Time
}

type taskState struct {
	status string
	err    string
}

// indexSink is the part of Meilisearch the queue talks to.
type indexSink interface {
	sinkReady() bool
	sendUpserts(ctx context.Context, index string, body []byte) (int64, error)
	sendDeletes(ctx context.Context, index string, ids []string) (int64, error)
	taskStates(ctx context.Context, uids []int64) (map[int64]taskState, error)
}

// IndexQueueConfigFromEnv reads the SEARCH_INDEX_* settings.
func IndexQueueConfigFromEnv() IndexQueueConfig {
	return IndexQueueConfig{
		FlushInterval:     time.Duration(settingInt("SEARCH_INDEX_FLUSH_INTERVAL_MS", int(defaultFlushInterval/time.Millisecond), 100, 60000)) * time.Millisecond,
		MaxDocs:           settingInt("SEARCH_INDEX_BATCH_MAX_DOCS", defaultBatchMaxDocs, 1, 10000),
		MaxBytes:          settingInt("SEARCH_INDEX_BATCH_MAX_BYTES", defaultBatchMaxBytes, 64<<10, 90<<20),
		MaxPending:        settingInt("SEARCH_INDEX_QUEUE_MAX", defaultQueueMax, 100, 1000000),
		MaxRetries:        settingInt("SEARCH_INDEX_MAX_RETRIES", defaultMaxRetries, 0, 20),
		TaskCheckInterval: time.Duration(settingInt("SEARCH_INDEX_TASK_CHECK_INTERVAL_MS", int(defaultTaskCheckInterval/time.Millisecond), int(minTaskCheckInterval/time.Millisecond), 600000)) * time.Millisecond,
	}
}

// settingInt reads name from the config file / environment, clamped.
func settingInt(name string, def, min, max int) int {
	raw, ok := config.C.Other[name]
	if !ok || strings.TrimSpace(raw) == "" {
		raw = os.Getenv(name)
	}
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func (c IndexQueueConfig) withDefaults() IndexQueueConfig {
	if c.FlushInterval <= 0 {
		c.FlushInterval = defaultFlushInterval
	}
	if c.MaxDocs <= 0 {
		c.MaxDocs = defaultBatchMaxDocs
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = defaultBatchMaxBytes
	}
	if c.MaxPending <= 0 {
		c.MaxPending = defaultQueueMax
	}
	if c.MaxRetries < 0 {
		c.MaxRetries = 0
	}
	if c.TaskCheckInterval <= 0 {
		c.TaskCheckInterval = defaultTaskCheckInterval
	}
	if c.BaseBackoff <= 0 {
		c.BaseBackoff = time.Second
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = defaultMaxBackoff
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = 15 * time.Second
	}
	return c
}

func newIndexQueue(sink indexSink, cfg IndexQueueConfig) *IndexQueue {
	return &IndexQueue{
		cfg:          cfg.withDefaults(),
		sink:         sink,
		pending:      map[string]map[string]queuedOp{},
		pendingBytes: map[string]int{},
		live:         map[opKey]*keyState{},
		kick:         make(chan struct{}, 1),
	}
}

// Upsert queues doc (any JSON-serializable document with an "id") for index.
func (q *IndexQueue) Upsert(index, id string, doc any) error {
	if id == "" {
		return errors.New("invalid document: id is required")
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	q.enqueue(index, queuedOp{id: id, doc: raw})
	return nil
}

// Delete queues the removal of document id from index.
func (q *IndexQueue) Delete(index, id string) error {
	if id == "" {
		return errors.New("id is required")
	}
	q.enqueue(index, queuedOp{id: id})
	return nil
}

func (q *IndexQueue) enqueue(index string, op queuedOp) {
	q.mu.Lock()
	if q.closed {
		q.stats.Dropped++
		q.mu.Unlock()
		return
	}
	docs := q.pending[index]
	if docs == nil {
		docs = map[string]queuedOp{}
		q.pending[index] = docs
	}
	prev, replaced := docs[op.id]
	if !replaced && q.pendingCount+q.retryOps >= q.cfg.MaxPending {
		q.stats.Dropped++
		logNow := time.Since(q.dropLogAt) >= 30*time.Second
		if logNow {
			q.dropLogAt = time.Now()
		}
		dropped := q.stats.Dropped
		q.mu.Unlock()
		if logNow {
			log.Warn("search index queue full (%d documents); dropping updates (%d dropped so far, the next backfill repairs them)", q.cfg.MaxPending, dropped)
		}
		return
	}
	q.seq++
	op.seq = q.seq
	k := opKey{index, op.id}
	ks := q.live[k]
	if ks == nil {
		ks = &keyState{}
		q.live[k] = ks
	}
	ks.newest = op.seq
	ks.refs++
	if replaced {
		ks.refs-- // the replaced op is gone
		q.pendingBytes[index] -= len(prev.doc) + len(prev.id)
		q.stats.Coalesced++
	} else {
		q.pendingCount++
	}
	docs[op.id] = op
	q.pendingBytes[index] += len(op.doc) + len(op.id)
	q.stats.Enqueued++
	full := len(docs) >= q.cfg.MaxDocs || q.pendingBytes[index] >= q.cfg.MaxBytes
	if !q.started {
		q.started = true
		q.stop, q.done = make(chan struct{}), make(chan struct{})
		go q.loop()
	}
	q.mu.Unlock()
	if full {
		select {
		case q.kick <- struct{}{}:
		default:
		}
	}
}

func (q *IndexQueue) loop() {
	defer close(q.done)
	timer := time.NewTimer(q.cfg.FlushInterval)
	defer timer.Stop()
	for {
		select {
		case <-q.stop:
			return
		case <-q.kick:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-timer.C:
		}
		q.runOnce(false)
		timer.Reset(q.cfg.FlushInterval)
	}
}

// Flush sends everything queued now, ignoring the retry backoff. It does not
// wait for Meilisearch to process the batches.
func (q *IndexQueue) Flush(ctx context.Context) error {
	return q.runOnceCtx(ctx, true)
}

// Close stops the background loop and makes one final attempt to send what
// is queued. Later enqueues are dropped.
func (q *IndexQueue) Close(ctx context.Context) error {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil
	}
	q.closed = true
	started := q.started
	q.mu.Unlock()
	if started {
		close(q.stop)
		select {
		case <-q.done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return q.runOnceCtx(ctx, true)
}

// Stats returns a snapshot of the counters.
func (q *IndexQueue) Stats() IndexQueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.stats
	s.Pending = q.pendingCount
	s.Inflight = len(q.inflight)
	s.RetryBatches = len(q.retries)
	return s
}

func (q *IndexQueue) runOnce(force bool) {
	_ = q.runOnceCtx(context.Background(), force)
}

// runOnceCtx sends pending documents and due retries, then checks task
// results if it is time. force ignores the backoff and retry delays and
// skips the task check (used by Flush and Close).
func (q *IndexQueue) runOnceCtx(ctx context.Context, force bool) error {
	q.flushMu.Lock()
	defer q.flushMu.Unlock()
	if !q.sink.sinkReady() {
		// Engine offline: keep the queue (bounded by MaxPending). The
		// reconnect backfill covers anything that is dropped meanwhile.
		q.mu.Lock()
		n := q.pendingCount + q.retryOps
		q.mu.Unlock()
		if n > 0 && force {
			return fmt.Errorf("meilisearch unavailable; %d queued search-index operations not sent", n)
		}
		return nil
	}
	now := time.Now()
	q.mu.Lock()
	if !force && now.Before(q.backoffUntil) {
		q.mu.Unlock()
		return nil
	}
	batches := q.takePendingLocked()
	var later []*indexBatch
	for _, b := range q.retries {
		if force || !now.Before(b.notBefore) {
			batches = append(batches, q.filterStaleLocked(b))
		} else {
			later = append(later, b)
		}
	}
	q.retries = later
	q.recountRetryOpsLocked()
	q.mu.Unlock()

	var sendErr error
	sentDocs := 0
	for _, b := range batches {
		if b == nil || len(b.ops) == 0 {
			continue
		}
		if sendErr != nil {
			// The engine just failed: do not hammer it with the rest.
			q.requeue(b, false, false, "")
			continue
		}
		if err := q.send(ctx, b); err != nil {
			sendErr = err
			permanent := isPermanentMeiliError(err)
			q.mu.Lock()
			q.stats.SendErrors++
			if !permanent {
				q.failures++
				q.backoffUntil = time.Now().Add(q.backoff(q.failures))
			}
			q.mu.Unlock()
			q.requeue(b, true, permanent, err.Error())
			if permanent {
				sendErr = nil // only this batch is bad; keep sending the rest
			}
			continue
		}
		sentDocs += len(b.ops)
	}
	q.mu.Lock()
	if sendErr == nil && len(batches) > 0 {
		q.failures = 0
	}
	if sentDocs > 0 {
		q.stats.LastFlushDocs = sentDocs
	}
	checkDue := !force && len(q.inflight) > 0 && now.Sub(q.lastTaskCheck) >= q.cfg.TaskCheckInterval
	q.mu.Unlock()
	if sendErr != nil {
		log.Warn("search index batch not sent, will retry: %v", sendErr)
	}
	if checkDue {
		q.checkTasks(ctx)
	}
	if force {
		q.mu.Lock()
		left := q.pendingCount + q.retryOps
		q.mu.Unlock()
		if left > 0 {
			if sendErr != nil {
				return fmt.Errorf("%d search-index operations not sent: %w", left, sendErr)
			}
			return fmt.Errorf("%d search-index operations not sent", left)
		}
	}
	return nil
}

// takePendingLocked moves the pending map into request-sized batches.
func (q *IndexQueue) takePendingLocked() []*indexBatch {
	if q.pendingCount == 0 {
		return nil
	}
	indexes := make([]string, 0, len(q.pending))
	for name := range q.pending {
		indexes = append(indexes, name)
	}
	sort.Strings(indexes)
	var out []*indexBatch
	for _, name := range indexes {
		var upserts, deletes []queuedOp
		for _, op := range q.pending[name] {
			if op.doc == nil {
				deletes = append(deletes, op)
			} else {
				upserts = append(upserts, op)
			}
		}
		out = append(out, q.chunk(name, false, upserts)...)
		out = append(out, q.chunk(name, true, deletes)...)
	}
	q.pending = map[string]map[string]queuedOp{}
	q.pendingBytes = map[string]int{}
	q.pendingCount = 0
	return out
}

func (q *IndexQueue) chunk(index string, del bool, ops []queuedOp) []*indexBatch {
	if len(ops) == 0 {
		return nil
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].seq < ops[j].seq })
	var out []*indexBatch
	cur := &indexBatch{index: index, del: del}
	size := 2
	for _, op := range ops {
		n := len(op.doc) + len(op.id) + 3
		if len(cur.ops) > 0 && (len(cur.ops) >= q.cfg.MaxDocs || size+n > q.cfg.MaxBytes) {
			out = append(out, cur)
			cur = &indexBatch{index: index, del: del}
			size = 2
		}
		cur.ops = append(cur.ops, op)
		size += n
	}
	return append(out, cur)
}

// filterStaleLocked drops ops of a retry batch that newer data supersedes.
func (q *IndexQueue) filterStaleLocked(b *indexBatch) *indexBatch {
	kept := b.ops[:0:0]
	for _, op := range b.ops {
		if ks := q.live[opKey{b.index, op.id}]; ks != nil && ks.newest > op.seq {
			q.releaseLocked(b.index, op)
			continue
		}
		kept = append(kept, op)
	}
	b.ops = kept
	return b
}

func (q *IndexQueue) send(ctx context.Context, b *indexBatch) error {
	rctx, cancel := context.WithTimeout(ctx, q.cfg.RequestTimeout)
	defer cancel()
	var uid int64
	var err error
	if b.del {
		ids := make([]string, len(b.ops))
		for i, op := range b.ops {
			ids[i] = op.id
		}
		uid, err = q.sink.sendDeletes(rctx, b.index, ids)
	} else {
		var body bytes.Buffer
		body.WriteByte('[')
		for i, op := range b.ops {
			if i > 0 {
				body.WriteByte(',')
			}
			body.Write(op.doc)
		}
		body.WriteByte(']')
		uid, err = q.sink.sendUpserts(rctx, b.index, body.Bytes())
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stats.Requests++
	if err != nil {
		return err
	}
	q.stats.DocsSent += uint64(len(b.ops))
	b.taskUID, b.sentAt = uid, time.Now()
	q.inflight = append(q.inflight, b)
	if len(q.inflight) > maxInflight {
		q.resolveLocked(q.inflight[0])
		q.inflight = q.inflight[1:]
	}
	return nil
}

// requeue schedules b again. counted increments its attempt counter; split
// bisects it so one bad document cannot keep failing the whole batch.
func (q *IndexQueue) requeue(b *indexBatch, counted, split bool, reason string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if counted {
		b.attempts++
	}
	if b.attempts > q.cfg.MaxRetries {
		q.stats.GaveUp++
		sample := make([]string, 0, 5)
		for i := 0; i < len(b.ops) && i < 5; i++ {
			sample = append(sample, b.ops[i].id)
		}
		log.Warn("search index: giving up on %d %s operations for %s after %d attempts (%s); ids %v; the next backfill repairs them",
			len(b.ops), map[bool]string{true: "delete", false: "upsert"}[b.del], b.index, b.attempts, reason, sample)
		q.resolveLocked(b)
		return
	}
	q.stats.Retried++
	delay := q.backoff(b.attempts)
	parts := []*indexBatch{b}
	if split && len(b.ops) > 1 {
		mid := len(b.ops) / 2
		parts = []*indexBatch{
			{index: b.index, del: b.del, ops: append([]queuedOp(nil), b.ops[:mid]...), attempts: b.attempts},
			{index: b.index, del: b.del, ops: append([]queuedOp(nil), b.ops[mid:]...), attempts: b.attempts},
		}
	}
	for _, p := range parts {
		p.taskUID, p.sentAt = 0, time.Time{}
		p.notBefore = time.Now().Add(delay)
		q.retries = append(q.retries, p)
	}
	q.recountRetryOpsLocked()
}

func (q *IndexQueue) recountRetryOpsLocked() {
	n := 0
	for _, b := range q.retries {
		n += len(b.ops)
	}
	q.retryOps = n
}

func (q *IndexQueue) backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := q.cfg.BaseBackoff
	for i := 1; i < attempt && d < q.cfg.MaxBackoff; i++ {
		d *= 2
	}
	if d > q.cfg.MaxBackoff {
		d = q.cfg.MaxBackoff
	}
	return d
}

// resolveLocked forgets the ops of a batch that is done (or abandoned).
func (q *IndexQueue) resolveLocked(b *indexBatch) {
	for _, op := range b.ops {
		q.releaseLocked(b.index, op)
	}
}

func (q *IndexQueue) releaseLocked(index string, op queuedOp) {
	k := opKey{index, op.id}
	if ks := q.live[k]; ks != nil {
		if ks.refs--; ks.refs <= 0 {
			delete(q.live, k)
		}
	}
}

// checkTasks asks Meilisearch about all outstanding batches in one request.
func (q *IndexQueue) checkTasks(ctx context.Context) {
	q.mu.Lock()
	q.lastTaskCheck = time.Now()
	uids := make([]int64, 0, len(q.inflight))
	for _, b := range q.inflight {
		if len(uids) >= maxTaskUIDsPerCheck {
			break
		}
		uids = append(uids, b.taskUID)
	}
	q.stats.TaskChecks++
	q.mu.Unlock()
	if len(uids) == 0 {
		return
	}
	rctx, cancel := context.WithTimeout(ctx, q.cfg.RequestTimeout)
	states, err := q.sink.taskStates(rctx, uids)
	cancel()
	if err != nil {
		log.Debug("search index task check failed: %v", err)
		return
	}
	asked := map[int64]bool{}
	for _, uid := range uids {
		asked[uid] = true
	}
	var failed []*indexBatch
	var reasons []string
	q.mu.Lock()
	kept := q.inflight[:0]
	now := time.Now()
	for _, b := range q.inflight {
		if !asked[b.taskUID] {
			kept = append(kept, b)
			continue
		}
		st, ok := states[b.taskUID]
		switch {
		case ok && st.status == string(meilisearch.TaskStatusSucceeded):
			q.resolveLocked(b)
		case ok && (st.status == string(meilisearch.TaskStatusFailed) || st.status == string(meilisearch.TaskStatusCanceled)):
			q.stats.TaskFailures++
			failed = append(failed, b)
			reasons = append(reasons, fmt.Sprintf("task %d %s: %s", b.taskUID, st.status, st.err))
		case now.Sub(b.sentAt) > inflightTTL:
			// Still enqueued (or unknown) after a long time: it is in
			// Meili's durable queue; stop tracking it.
			q.resolveLocked(b)
		default:
			kept = append(kept, b)
		}
	}
	q.inflight = kept
	q.mu.Unlock()
	for i, b := range failed {
		log.Warn("search index batch failed (%d docs, %s): %s", len(b.ops), b.index, reasons[i])
		q.requeue(b, true, true, reasons[i])
	}
}

// isPermanentMeiliError reports a request Meilisearch rejected outright
// (4xx other than 408/429): resending the same payload cannot succeed.
func isPermanentMeiliError(err error) bool {
	var me *meilisearch.Error
	if errors.As(err, &me) {
		return me.StatusCode >= 400 && me.StatusCode < 500 && me.StatusCode != 408 && me.StatusCode != 429
	}
	return false
}

// --- MeilisearchClient as the queue's sink ---------------------------------

func (m *MeilisearchClient) sinkReady() bool { return m.IsEnabled() }

func (m *MeilisearchClient) sendUpserts(ctx context.Context, index string, body []byte) (int64, error) {
	task, err := m.client.Index(m.uid(index)).AddDocumentsWithContext(ctx, body, nil)
	if err != nil {
		return 0, err
	}
	return task.TaskUID, nil
}

func (m *MeilisearchClient) sendDeletes(ctx context.Context, index string, ids []string) (int64, error) {
	task, err := m.client.Index(m.uid(index)).DeleteDocumentsWithContext(ctx, ids, nil)
	if err != nil {
		return 0, err
	}
	return task.TaskUID, nil
}

func (m *MeilisearchClient) taskStates(ctx context.Context, uids []int64) (map[int64]taskState, error) {
	res, err := m.client.GetTasksWithContext(ctx, &meilisearch.TasksQuery{UIDS: uids, Limit: int64(len(uids))})
	if err != nil {
		return nil, err
	}
	out := make(map[int64]taskState, len(res.Results))
	for _, t := range res.Results {
		uid := t.UID
		if uid == 0 {
			uid = t.TaskUID
		}
		out[uid] = taskState{status: string(t.Status), err: t.Error.Message}
	}
	return out, nil
}

// IndexQueue returns the client's batching queue (created on first use).
func (m *MeilisearchClient) IndexQueue() *IndexQueue {
	m.queueOnce.Do(func() {
		m.queue.Store(newIndexQueue(m, IndexQueueConfigFromEnv()))
	})
	return m.queue.Load()
}

// QueueAccount queues an account document. It never blocks on Meilisearch;
// with the engine disabled it does nothing (the reconnect backfill covers it).
func (m *MeilisearchClient) QueueAccount(doc *AccountSearchDocument) error {
	if !m.IsEnabled() {
		return nil
	}
	if doc == nil || doc.ID == "" {
		return errors.New("invalid document: id is required")
	}
	return m.IndexQueue().Upsert(IndexAccounts, doc.ID, doc)
}

// QueueStatus queues a status document.
func (m *MeilisearchClient) QueueStatus(doc *StatusSearchDocument) error {
	if !m.IsEnabled() {
		return nil
	}
	if doc == nil || doc.ID == "" {
		return errors.New("invalid document: id is required")
	}
	return m.IndexQueue().Upsert(IndexStatuses, doc.ID, doc)
}

// QueueHashtag queues a hashtag document. Invalid documents are skipped, as
// one would fail a whole batch.
func (m *MeilisearchClient) QueueHashtag(doc *HashtagSearchDocument) error {
	if !m.IsEnabled() {
		return nil
	}
	if doc == nil || doc.ID == "" {
		return errors.New("invalid document: id is required")
	}
	if !ValidHashtagDocument(doc) {
		log.Warn("search: skip hashtag document with invalid id/name %q", doc.Name)
		return nil
	}
	return m.IndexQueue().Upsert(IndexHashtags, doc.ID, doc)
}

// QueueDelete queues the removal of document id from a logical index
// (IndexAccounts, IndexStatuses or IndexHashtags).
func (m *MeilisearchClient) QueueDelete(index, id string) error {
	if !m.IsEnabled() {
		return nil
	}
	return m.IndexQueue().Delete(index, id)
}

// ShutdownIndexQueue flushes queued search-index writes of the shared client
// and stops its queue. Call it on graceful shutdown.
func ShutdownIndexQueue(ctx context.Context) error {
	m := meiliClient
	if m == nil {
		return nil
	}
	q := m.queue.Load()
	if q == nil {
		return nil
	}
	return q.Close(ctx)
}
