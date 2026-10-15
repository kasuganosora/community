package admin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func seedQueue(t *testing.T) *models.Account {
	t.Helper()
	db, a := operationsDB(t)
	require.NoError(t, db.AutoMigrate(&models.FeedEvent{}, &models.FeedBatch{}))
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Minute)
	rows := []activitypub.Delivery{
		{ID: 1, TargetInbox: "https://bad.example/inbox", Status: "failed", Retries: 9, LastError: "remote returned error: 530 https://bad.example/x?t=SECRET"},
		{ID: 2, TargetInbox: "https://bad.example/users/a/inbox", Status: "pending", Retries: 2, NextRetryAt: &future},
		{ID: 3, TargetInbox: "https://ok.example/inbox", Status: "pending", NextRetryAt: &past},
		{ID: 4, TargetInbox: "https://ok.example/inbox", Status: "processing"},
		{ID: 5, TargetInbox: "https://ok.example/inbox", Status: "delivered", Payload: `{"type":"Create"}`},
		{ID: 6, TargetInbox: "https://slow.example/inbox", Status: "pending", Retries: 1, NextRetryAt: &future},
	}
	for i := range rows {
		rows[i].ActivityID = "a"
		if rows[i].Payload == "" {
			rows[i].Payload = `{"type":"Follow","content":"PRIVATE"}`
		}
		require.NoError(t, db.Create(&rows[i]).Error)
	}
	return a
}

func TestQueueStatsCountsDerivedStates(t *testing.T) {
	queueSamplerAutostart = false
	a := seedQueue(t)
	c, w := newGetContext(t, nil, "")
	c.Set("account", a)
	FederationQueueStats(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var body struct {
		Queues []struct {
			Kind   string
			Counts queueCounts
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "delivery", body.Queues[0].Kind)
	require.Equal(t, queueCounts{Active: 1, Waiting: 1, Delayed: 2, Failed: 1, Completed: 1}, body.Queues[0].Counts)
	require.Len(t, body.Queues, 4)
}

func TestQueueSamplerRingBuffer(t *testing.T) {
	s := &queueSampler{last: map[string]queueCounts{}, series: map[string][]queuePoint{}}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.record("x", queueCounts{Completed: 10, Failed: 3}, base)
	for i := 1; i <= queueSeriesLen+5; i++ {
		s.record("x", queueCounts{Completed: int64(10 + i*2), Failed: 3, Waiting: 4}, base.Add(time.Duration(i)*time.Minute))
	}
	pts := s.snapshot("x")
	require.Len(t, pts, queueSeriesLen)
	require.EqualValues(t, 2, pts[0].Processed)
	require.EqualValues(t, 0, pts[0].Failed)
	require.EqualValues(t, 4, pts[0].Waiting)
}

func TestQueueHostsAndBulkActions(t *testing.T) {
	queueSamplerAutostart = false
	a := seedQueue(t)
	c, w := newGetContext(t, nil, "kind=delivery")
	c.Set("account", a)
	FederationQueueHosts(c)
	require.Equal(t, 200, w.Code)
	require.NotContains(t, w.Body.String(), "SECRET")
	var hosts struct {
		Items []struct {
			Host                   string
			Failed, Delayed, Total int64
		}
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &hosts))
	require.Equal(t, "bad.example", hosts.Items[0].Host)
	require.EqualValues(t, 1, hosts.Items[0].Failed)
	require.EqualValues(t, 1, hosts.Items[0].Delayed)
	require.Len(t, hosts.Items, 2)

	post := func(kind string, body gin.H) (int, string) {
		c, w := newJSONContext(t, "POST", gin.Params{{Key: "kind", Value: kind}}, body)
		c.Set("account", a)
		FederationQueueBulk(c)
		return w.Code, w.Body.String()
	}
	code, _ := post("delivery", gin.H{"action": "clear"})
	require.Equal(t, 422, code)
	code, _ = post("feed-event", gin.H{"action": "promote"})
	require.Equal(t, 422, code)
	code, _ = post("delivery", gin.H{"action": "promote", "host": "bad.example/../"})
	require.Equal(t, 422, code)
	code, out := post("delivery", gin.H{"action": "promote", "host": "slow.example"})
	require.Equal(t, 200, code)
	require.JSONEq(t, `{"updated":1}`, out)
	code, out = post("delivery", gin.H{"action": "clear", "confirm": true, "host": "bad.example"})
	require.Equal(t, 200, code)
	require.JSONEq(t, `{"updated":2}`, out)

	c, w = newGetContext(t, nil, "kind=delivery&state=waiting")
	c.Set("account", a)
	FederationJobs(c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"total":2`)
	c, w = newGetContext(t, nil, "kind=delivery&q=ok.example&state=delivered")
	c.Set("account", a)
	FederationJobs(c)
	require.Contains(t, w.Body.String(), `"activity_type":"Create"`)
	require.NotContains(t, w.Body.String(), "PRIVATE")
}
