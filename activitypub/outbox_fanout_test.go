package activitypub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

// One follower inbox answering 502 must not fail the Create for everyone else.
// The bad target is recorded as its own failed delivery (and named in the
// log); the outbox event is processed and the healthy inbox is still queued.
func TestOutboundCreateContinuesAfterRecipient502(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.Relay{}))
	author := outboundAccount(t, conn, "writer", false)
	good := outboundAccount(t, conn, "good", true)
	bad := outboundAccount(t, conn, "bad", true)
	require.NoError(t, conn.Create(&models.Follow{Id: 11, AccountId: good.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	require.NoError(t, conn.Create(&models.Follow{Id: 12, AccountId: bad.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	status := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "hello federation"}}
	require.NoError(t, conn.Create(status).Error)
	_, err := localAccountKey(author)
	require.NoError(t, err)

	logger := log.GetLogObject()
	previousOut := logger.Out
	var logged bytes.Buffer
	logger.SetOutput(&logged)
	t.Cleanup(func() { logger.SetOutput(previousOut) })

	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	postCode := 202
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			if strings.Contains(r.URL.Path, "/actors/bad") {
				return outboundResponse(502, "bad gateway"), nil
			}
			body := `{"id":"` + r.URL.String() + `","inbox":"` + r.URL.String() + `/inbox"}`
			return outboundResponse(200, body), nil
		}
		return outboundResponse(postCode, "unavailable"), nil
	})}

	(&OutboundWorker{}).processPending()

	events := outboxEvents(t, conn)
	require.Len(t, events, 1)
	require.Equal(t, "processed", events[0].State, "one 502 must not fail the whole outbox event")
	require.ElementsMatch(t, []uint64{good.Id, bad.Id}, events[0].RecipientIDs)
	require.Empty(t, events[0].LastError)
	require.Equal(t, 0, events[0].Attempts)

	var deliveries []Delivery
	require.NoError(t, conn.Order("id").Find(&deliveries).Error)
	require.Len(t, deliveries, 2)
	activityID := systemsettings.GetBaseURL() + "/users/writer/activities/" + strconv.FormatUint(events[0].ID, 10)
	byInbox := map[string]Delivery{}
	for _, d := range deliveries {
		byInbox[d.TargetInbox] = d
		require.Equal(t, activityID, d.ActivityID)
	}
	healthy := byInbox[good.OriginId+"/inbox"]
	require.Equal(t, DeliveryStatusPending, healthy.Status)
	require.Empty(t, healthy.LastError)
	failed := byInbox[bad.OriginId]
	require.Equal(t, DeliveryStatusFailed, failed.Status)
	require.Contains(t, failed.LastError, "unexpected status code: 502")
	require.Contains(t, logged.String(), "Federation outbox")
	require.Contains(t, logged.String(), bad.OriginId)
	require.Contains(t, logged.String(), "502")

	// A later POST failure stays on that delivery. The log names its inbox.
	logged.Reset()
	postCode = 530
	NewDeliveryQueue(nil, "", 0).processPending()
	require.NoError(t, conn.First(&healthy, healthy.ID).Error)
	require.Equal(t, DeliveryStatusPending, healthy.Status)
	require.Equal(t, 1, healthy.Retries)
	require.Contains(t, healthy.LastError, "530")
	require.Contains(t, logged.String(), "Federation delivery")
	require.Contains(t, logged.String(), good.OriginId+"/inbox")

	router := gin.New()
	router.GET("/users/:username/activities/:id", OutboxActivity)
	base := systemsettings.GetBaseURL()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, base+"/users/writer/activities/"+strconv.FormatUint(events[0].ID, 10), nil)
	req.Header.Set("Accept", "application/activity+json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Header().Get("Content-Type"), "application/activity+json")
	var served map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &served))
	var queued map[string]any
	require.NoError(t, json.Unmarshal([]byte(healthy.Payload), &queued))
	require.Equal(t, "Create", served["type"])
	require.Equal(t, activityID, served["id"])
	require.Equal(t, queued["id"], served["id"])
	object := served["object"].(map[string]any)
	require.Equal(t, base+"/users/writer/statuses/"+strconv.FormatUint(status.Id, 10), object["id"])
	require.Equal(t, queued["object"].(map[string]any)["id"], object["id"])

	missing := httptest.NewRecorder()
	router.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, base+"/users/writer/activities/999999", nil))
	require.Equal(t, http.StatusNotFound, missing.Code)
}

func actorGetHasOwnDeadline(r *http.Request) bool {
	deadline, ok := r.Context().Deadline()
	if !ok {
		return false
	}
	remaining := time.Until(deadline)
	return remaining > 0 && remaining <= outboxRecipientTimeout
}

// Actor GETs run one goroutine per recipient, and errgroup.SetLimit caps how
// many of those requests are in flight together.
func TestOutboundFanoutLimitsConcurrentActorGets(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.Relay{}))
	author := outboundAccount(t, conn, "writer", false)
	const recipients = 6
	for i := range recipients {
		account := outboundAccount(t, conn, fmt.Sprintf("peer%d", i), true)
		require.NoError(t, conn.Create(&models.Follow{Id: uint64(100 + i), AccountId: account.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	}
	require.NoError(t, conn.Create(&models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "fanout"}}).Error)

	prevLimit, prevTimeout := outboxExpandConcurrency, outboxRecipientTimeout
	outboxExpandConcurrency = 2
	outboxRecipientTimeout = 2 * time.Second
	t.Cleanup(func() {
		outboxExpandConcurrency, outboxRecipientTimeout = prevLimit, prevTimeout
	})

	var current, peak atomic.Int32
	var deadlineMiss atomic.Bool
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			return outboundResponse(200, ""), nil
		}
		if !actorGetHasOwnDeadline(r) {
			deadlineMiss.Store(true)
		}
		n := current.Add(1)
		for {
			seen := peak.Load()
			if n <= seen || peak.CompareAndSwap(seen, n) {
				break
			}
		}
		time.Sleep(80 * time.Millisecond)
		current.Add(-1)
		body := `{"id":"` + r.URL.String() + `","inbox":"` + r.URL.String() + `/inbox"}`
		return outboundResponse(200, body), nil
	})}

	started := time.Now()
	(&OutboundWorker{}).processPending()
	require.Less(t, time.Since(started), time.Second)
	require.False(t, deadlineMiss.Load(), "each actor GET must carry its own deadline")
	require.Equal(t, int32(2), peak.Load())

	events := outboxEvents(t, conn)
	require.Len(t, events, 1)
	require.Equal(t, "processed", events[0].State)
	require.Empty(t, events[0].LastError)
	var deliveries []Delivery
	require.NoError(t, conn.Find(&deliveries).Error)
	require.Len(t, deliveries, recipients)
	for _, delivery := range deliveries {
		require.Equal(t, DeliveryStatusPending, delivery.Status)
	}
}

// A hung actor GET times out on its own request. That recipient is a failed
// delivery; peers that answered (including a 502) are still queued and the
// outbox event is processed instead of left with zero rows.
func TestOutboundFanoutTimesOutHungPeerWithoutDroppingSuccesses(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.Relay{}))
	author := outboundAccount(t, conn, "writer", false)
	names := []string{"fast", "bad", "slow", "share"}
	followers := make([]*models.Account, 0, len(names))
	for i, name := range names {
		account := outboundAccount(t, conn, name, true)
		followers = append(followers, account)
		require.NoError(t, conn.Create(&models.Follow{Id: uint64(100 + i), AccountId: account.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	}
	require.NoError(t, conn.Create(&models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "partial fanout"}}).Error)

	prevLimit, prevTimeout := outboxExpandConcurrency, outboxRecipientTimeout
	outboxExpandConcurrency = 4
	outboxRecipientTimeout = 200 * time.Millisecond
	t.Cleanup(func() {
		outboxExpandConcurrency, outboxRecipientTimeout = prevLimit, prevTimeout
	})

	var deadlineMiss atomic.Bool
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	// No Client.Timeout here: the fanout must install one per actor GET.
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet {
			return outboundResponse(200, ""), nil
		}
		if !actorGetHasOwnDeadline(r) {
			deadlineMiss.Store(true)
		}
		if strings.Contains(r.URL.Path, "/actors/bad") {
			return outboundResponse(502, "bad gateway"), nil
		}
		if strings.Contains(r.URL.Path, "/actors/slow") {
			<-r.Context().Done()
			return nil, r.Context().Err()
		}
		inbox := r.URL.String() + "/inbox"
		if strings.Contains(r.URL.Path, "/actors/share") {
			inbox = followers[0].OriginId + "/inbox"
		}
		body := `{"id":"` + r.URL.String() + `","inbox":"` + inbox + `"}`
		return outboundResponse(200, body), nil
	})}

	started := time.Now()
	(&OutboundWorker{}).processPending()
	elapsed := time.Since(started)
	require.False(t, deadlineMiss.Load(), "each actor GET must carry its own deadline")
	require.Less(t, elapsed, 3*time.Second, "a hung peer must not stall the fanout")
	require.GreaterOrEqual(t, elapsed, 100*time.Millisecond, "the slow GET has to hit its own timeout")

	events := outboxEvents(t, conn)
	require.Len(t, events, 1)
	require.Equal(t, "processed", events[0].State)
	require.Equal(t, 0, events[0].Attempts)
	require.Empty(t, events[0].LastError)
	require.Len(t, events[0].RecipientIDs, len(followers))

	var deliveries []Delivery
	require.NoError(t, conn.Order("id").Find(&deliveries).Error)
	require.Len(t, deliveries, 3, "fast and share share one inbox; bad and slow are their own failed rows")
	byInbox := map[string]Delivery{}
	for _, delivery := range deliveries {
		byInbox[delivery.TargetInbox] = delivery
	}
	shared := byInbox[followers[0].OriginId+"/inbox"]
	require.Equal(t, DeliveryStatusPending, shared.Status)
	require.Empty(t, shared.LastError)
	var auth DeliveryAuthorization
	require.NoError(t, json.Unmarshal([]byte(shared.Authorization), &auth))
	require.ElementsMatch(t, []uint64{followers[0].Id, followers[3].Id}, auth.RecipientIDs)

	bad := byInbox[followers[1].OriginId]
	require.Equal(t, DeliveryStatusFailed, bad.Status)
	require.Contains(t, bad.LastError, "502")

	slow := byInbox[followers[2].OriginId]
	require.Equal(t, DeliveryStatusFailed, slow.Status)
	require.Contains(t, slow.LastError, "deadline exceeded")
}
