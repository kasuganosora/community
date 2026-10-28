package activitypub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

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
