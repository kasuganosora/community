package admin

import (
	"context"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// queueSpec describes how a persisted job table maps onto the generic
// Active / Waiting / Delayed / Failed model used by the admin queue UI.
type queueSpec struct {
	Kind, Label           string
	model                 any
	state, next, attempts string
	done                  string
	bulk                  bool
}

var federationQueues = []queueSpec{
	{Kind: "delivery", Label: "ActivityPub deliver", model: &activitypub.Delivery{}, state: "status", next: "next_retry_at", attempts: "retries", done: "delivered", bulk: true},
	{Kind: "outbox", Label: "Federation outbox", model: &models.FederationOutbox{}, state: "state", next: "next_attempt_at", attempts: "attempts", done: "processed", bulk: true},
	{Kind: "feed-event", Label: "Feed events", model: &models.FeedEvent{}, state: "state", next: "next_attempt_at", attempts: "attempts", done: "complete"},
	{Kind: "feed-batch", Label: "Feed audience batches", model: &models.FeedBatch{}, state: "state", next: "next_attempt_at", attempts: "attempts", done: "complete"},
}

func queueByKind(kind string) (queueSpec, bool) {
	for _, q := range federationQueues {
		if q.Kind == kind {
			return q, true
		}
	}
	return queueSpec{}, false
}

type queueCounts struct {
	Active    int64 `json:"active"`
	Waiting   int64 `json:"waiting"`
	Delayed   int64 `json:"delayed"`
	Failed    int64 `json:"failed"`
	Completed int64 `json:"completed"`
	Cancelled int64 `json:"cancelled"`
}

type queuePoint struct {
	At        time.Time `json:"at"`
	Processed int64     `json:"processed"`
	Failed    int64     `json:"failed"`
	Active    int64     `json:"active"`
	Waiting   int64     `json:"waiting"`
	Delayed   int64     `json:"delayed"`
}

// countQueue issues one GROUP BY over the indexed state column.
func countQueue(conn *gorm.DB, q queueSpec, now time.Time) (queueCounts, error) {
	var rows []struct {
		State   string
		Delayed int64
		N       int64
	}
	err := conn.Model(q.model).Select(q.state+" AS state, SUM(CASE WHEN "+q.next+" > ? THEN 1 ELSE 0 END) AS delayed, COUNT(*) AS n", now).Group(q.state).Scan(&rows).Error
	var c queueCounts
	for _, r := range rows {
		switch r.State {
		case "pending":
			c.Delayed += r.Delayed
			c.Waiting += r.N - r.Delayed
		case "processing":
			c.Active += r.N
		case "failed":
			c.Failed += r.N
		case "cancelled":
			c.Cancelled += r.N
		case q.done:
			c.Completed += r.N
		}
	}
	return c, err
}

const queueSeriesLen = 60

// queueSampler keeps a per-minute in-memory ring buffer of counter deltas.
// It is process-local and resets on restart; that is acceptable for an ops view.
type queueSampler struct {
	mu     sync.Mutex
	last   map[string]queueCounts
	lastAt map[string]time.Time
	series map[string][]queuePoint
}

var sampler = &queueSampler{last: map[string]queueCounts{}, series: map[string][]queuePoint{}}
var samplerOnce sync.Once
var queueSamplerAutostart = true

func positive(v int64) int64 {
	if v < 0 {
		return 0
	}
	return v
}

func (s *queueSampler) record(kind string, c queueCounts, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastAt == nil {
		s.lastAt = map[string]time.Time{}
	}
	// A request and the background collector may finish out of order.
	if !at.After(s.lastAt[kind]) {
		return
	}
	prev, seen := s.last[kind]
	previousAt := s.lastAt[kind]
	s.last[kind], s.lastAt[kind] = c, at
	p := queuePoint{At: at.Truncate(time.Minute), Active: c.Active, Waiting: c.Waiting, Delayed: c.Delayed}
	if seen && !previousAt.Before(p.At.Add(-time.Duration(queueSeriesLen-1)*time.Minute)) {
		p.Processed = positive(c.Completed - prev.Completed)
		p.Failed = positive(c.Failed - prev.Failed)
	}
	list := s.series[kind]
	// Do not present old samples as the last hour after a collection gap.
	cutoff := p.At.Add(-time.Duration(queueSeriesLen-1) * time.Minute)
	for len(list) > 0 && list[0].At.Before(cutoff) {
		list = list[1:]
	}
	if n := len(list); n > 0 && list[n-1].At.Equal(p.At) {
		// Polls within a minute add to the bucket instead of erasing its delta.
		p.Processed += list[n-1].Processed
		p.Failed += list[n-1].Failed
		list[n-1] = p
	} else {
		list = append(list, p)
	}
	if len(list) > queueSeriesLen {
		list = list[len(list)-queueSeriesLen:]
	}
	s.series[kind] = list
}

func (s *queueSampler) snapshot(kind string) []queuePoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]queuePoint{}, s.series[kind]...)
}

func sampleQueues(conn *gorm.DB, now time.Time) {
	for _, q := range federationQueues {
		if c, err := countQueue(conn, q, now); err == nil {
			sampler.record(q.Kind, c, now)
		}
	}
}

func startQueueSampler() {
	samplerOnce.Do(func() {
		go func() {
			tick := time.NewTicker(time.Minute)
			defer tick.Stop()
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				sampleQueues(db.GetDB().WithContext(ctx), time.Now())
				cancel()
				<-tick.C
			}
		}()
	})
}

// FederationQueueStats: GET /admin/federation/queue-stats
func FederationQueueStats(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionDevops) {
		return
	}
	if queueSamplerAutostart {
		startQueueSampler()
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	now := time.Now()
	out := []gin.H{}
	for _, q := range federationQueues {
		counts, err := countQueue(conn, q, now)
		if operationError(c, err) {
			return
		}
		// Seed and advance history from successful reads too: charts never depend
		// on a separate collector successfully completing its first two ticks.
		sampler.record(q.Kind, counts, now)
		out = append(out, gin.H{"kind": q.Kind, "label": q.Label, "counts": counts, "series": sampler.snapshot(q.Kind), "bulk_actions": q.bulk, "hosts": q.Kind == "delivery"})
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(200, gin.H{"queues": out, "generated_at": now, "interval_seconds": 60, "inbox_queue": false})
}

func inboxHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// FederationQueueHosts: GET /admin/federation/queue-hosts?kind=delivery
// Groups failed and retry-scheduled deliveries by target host.
func FederationQueueHosts(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionDevops) {
		return
	}
	if c.DefaultQuery("kind", "delivery") != "delivery" {
		c.JSON(422, gin.H{"error": "host summary is only available for delivery"})
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	now := time.Now()
	var rows []struct {
		TargetInbox string
		Status      string
		Delayed     int64
		N           int64
		LastAt      string
	}
	if operationError(c, conn.Model(&activitypub.Delivery{}).Select("target_inbox, status, SUM(CASE WHEN next_retry_at > ? THEN 1 ELSE 0 END) AS delayed, COUNT(*) AS n", now).
		Where("status = ? OR (status = ? AND retries > 0)", "failed", "pending").Group("target_inbox, status").Limit(5000).Scan(&rows).Error) {
		return
	}
	type host struct {
		Host      string `json:"host"`
		Failed    int64  `json:"failed"`
		Delayed   int64  `json:"delayed"`
		Total     int64  `json:"total"`
		LastError string `json:"last_error"`
		inbox     string
	}
	byHost := map[string]*host{}
	for _, r := range rows {
		name := inboxHost(r.TargetInbox)
		if name == "" {
			continue
		}
		h := byHost[name]
		if h == nil {
			h = &host{Host: name, inbox: r.TargetInbox}
			byHost[name] = h
		}
		if r.Status == "failed" {
			h.Failed += r.N
		} else {
			h.Delayed += r.N
		}
		h.Total += r.N
	}
	list := make([]*host, 0, len(byHost))
	for _, h := range byHost {
		list = append(list, h)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Total != list[j].Total {
			return list[i].Total > list[j].Total
		}
		return list[i].Host < list[j].Host
	})
	if len(list) > 50 {
		list = list[:50]
	}
	for _, h := range list {
		var d activitypub.Delivery
		if conn.Select("last_error").Where("target_inbox = ? AND last_error <> ''", h.inbox).Order("id DESC").Limit(1).Find(&d).Error == nil {
			h.LastError = safeJobError(d.LastError)
		}
	}
	c.JSON(200, gin.H{"items": list})
}

var queueHostPattern = regexp.MustCompile(`^[a-z0-9.-]{1,253}(:[0-9]{1,5})?$`)

func hostScope(q *gorm.DB, host string) *gorm.DB {
	return q.Where("(target_inbox LIKE ? OR target_inbox LIKE ? OR target_inbox = ? OR target_inbox = ?)", "https://"+host+"/%", "http://"+host+"/%", "https://"+host, "http://"+host)
}

// FederationQueueBulk: POST /admin/federation/queue-bulk/:kind
// action=promote runs every retry-scheduled job now; action=clear cancels all
// pending and failed jobs (requires confirm). Optional host limits delivery.
func FederationQueueBulk(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionDevops) {
		return
	}
	q, ok := queueByKind(c.Param("kind"))
	if !ok || !q.bulk {
		c.JSON(422, gin.H{"error": "bulk actions are not available for this queue"})
		return
	}
	var body struct {
		Action  string `json:"action"`
		Confirm bool   `json:"confirm"`
		Host    string `json:"host"`
	}
	if c.ShouldBindJSON(&body) != nil || (body.Action != "promote" && body.Action != "clear") || (body.Action == "clear" && !body.Confirm) {
		c.JSON(422, gin.H{"error": "promote or confirmed clear required"})
		return
	}
	body.Host = strings.ToLower(strings.TrimSpace(body.Host))
	if body.Host != "" && (q.Kind != "delivery" || !queueHostPattern.MatchString(body.Host)) {
		c.JSON(422, gin.H{"error": "invalid host"})
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	now := time.Now()
	tx := conn.Model(q.model)
	if body.Host != "" {
		tx = hostScope(tx, body.Host)
	}
	var r *gorm.DB
	if body.Action == "promote" {
		r = tx.Where(q.state+" = ? AND "+q.next+" > ?", "pending", now).Update(q.next, now)
	} else {
		r = tx.Where(q.state+" IN ?", []string{"pending", "failed"}).Update(q.state, "cancelled")
	}
	if operationError(c, r.Error) {
		return
	}
	c.JSON(200, gin.H{"updated": r.RowsAffected})
}
