package activitypub

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// threadsOrigin serves a note the way Threads does: unsigned GETs get
// `unsigned` (404), signed GETs get the note unless `status` overrides.
type threadsOrigin struct {
	mu       sync.Mutex
	unsigned int
	status   int // forced status for every request when non-zero
	signed   int
}

const threadsNote = "https://threads.test/@gihyojp/post/1"

func threadsNoteDoc() map[string]any {
	return map[string]any{
		"id": threadsNote, "type": "Note", "attributedTo": "https://threads.test/ap/users/gihyojp",
		"content": "<p>hello</p>", "published": "2026-10-18T00:00:00Z",
		"to": []any{"https://www.w3.org/ns/activitystreams#Public"},
	}
}

func (o *threadsOrigin) serve(r *http.Request) *http.Response {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.status != 0 {
		return outboundResponse(o.status, "")
	}
	if r.Header.Get("Signature") == "" {
		o.unsigned++
		return outboundResponse(http.StatusNotFound, "")
	}
	o.signed++
	data, _ := json.Marshal(threadsNoteDoc())
	return outboundResponse(200, string(data))
}

type announceFixture struct {
	w       *relayWorld
	booster *models.Account
	origin  *threadsOrigin
}

func newAnnounceFixture(t *testing.T) *announceFixture {
	w := newRelayWorld(t)
	maid, threads := "maid.test", "threads.test"
	booster := &models.Account{Username: "luna", Domain: &maid, OriginId: "https://maid.test/users/luna"}
	author := &models.Account{Username: "gihyojp", Domain: &threads, OriginId: "https://threads.test/ap/users/gihyojp"}
	require.NoError(t, w.conn.Create(booster).Error)
	require.NoError(t, w.conn.Create(author).Error)
	o := &threadsOrigin{}
	withFetchTransport(t, func(r *http.Request) (*http.Response, error) { return o.serve(r), nil }, testSigner(t))
	return &announceFixture{w: w, booster: booster, origin: o}
}

func (f *announceFixture) announce(t *testing.T, object any) int {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/inbox", nil)
	ctx := context.WithValue(context.Background(), actorKey, f.booster.OriginId)
	handleAnnounceActivity(ctx, c, map[string]any{"type": "Announce", "id": "https://maid.test/notes/9/activity", "actor": f.booster.OriginId, "object": object}, nil)
	return rec.Code
}

func (f *announceFixture) reblogs(t *testing.T) int64 {
	var n int64
	require.NoError(t, f.w.conn.Model(&models.Status{}).Where("account_id = ? AND reblog_id IS NOT NULL", f.booster.Id).Count(&n).Error)
	return n
}

func (f *announceFixture) retries(t *testing.T) []AnnounceRetry {
	var rows []AnnounceRetry
	require.NoError(t, f.w.conn.Find(&rows).Error)
	return rows
}

// Threads answers 404 to unsigned GETs: the boost must be fetched signed.
func TestAnnounceOfThreadsPostFetchesSigned(t *testing.T) {
	f := newAnnounceFixture(t)
	require.Equal(t, 200, f.announce(t, threadsNote))
	require.Equal(t, int64(1), f.reblogs(t))
	require.Equal(t, 1, f.origin.signed)
	require.Empty(t, f.retries(t))
}

func TestAnnounceFetchFailureIsRetriedWithBackoff(t *testing.T) {
	f := newAnnounceFixture(t)
	f.origin.status = http.StatusServiceUnavailable
	require.Equal(t, 200, f.announce(t, threadsNote))
	require.Zero(t, f.reblogs(t))
	rows := f.retries(t)
	require.Len(t, rows, 1)
	require.Equal(t, "https://maid.test/notes/9/activity", rows[0].ActivityID)
	require.Equal(t, f.booster.OriginId, rows[0].Actor)

	// Not due yet.
	require.NoError(t, ProcessAnnounceRetries(context.Background(), time.Now(), 10))
	require.Equal(t, 0, f.retries(t)[0].Attempts)

	// Still failing: attempt counted and rescheduled further out.
	require.NoError(t, ProcessAnnounceRetries(context.Background(), time.Now().Add(time.Hour), 10))
	rows = f.retries(t)
	require.Len(t, rows, 1)
	require.Equal(t, 1, rows[0].Attempts)
	require.True(t, rows[0].NextAttemptAt.After(time.Now().Add(time.Hour)))

	// Origin recovers.
	f.origin.mu.Lock()
	f.origin.status = 0
	f.origin.mu.Unlock()
	require.NoError(t, ProcessAnnounceRetries(context.Background(), time.Now().Add(24*time.Hour), 10))
	require.Equal(t, int64(1), f.reblogs(t))
	require.Empty(t, f.retries(t))
}

func TestAnnounceRetriesAreCapped(t *testing.T) {
	f := newAnnounceFixture(t)
	f.origin.status = http.StatusServiceUnavailable
	f.announce(t, threadsNote)
	at := time.Now()
	for i := 0; i < len(announceRetryBackoff); i++ {
		at = at.Add(24 * time.Hour)
		require.NoError(t, ProcessAnnounceRetries(context.Background(), at, 10))
	}
	require.Empty(t, f.retries(t))
	require.Zero(t, f.reblogs(t))
}

func TestAnnounceOfGoneObjectIsNotRetried(t *testing.T) {
	f := newAnnounceFixture(t)
	f.origin.status = http.StatusGone
	require.Equal(t, 200, f.announce(t, threadsNote))
	require.Empty(t, f.retries(t))

	// A queued retry that turns 410 is dropped.
	f.origin.status = http.StatusServiceUnavailable
	f.w.conn.Create(&AnnounceRetry{ActivityID: "a2", Actor: f.booster.OriginId, ObjectURI: threadsNote, NextAttemptAt: time.Now()})
	f.origin.status = http.StatusGone
	require.NoError(t, ProcessAnnounceRetries(context.Background(), time.Now().Add(time.Minute), 10))
	require.Empty(t, f.retries(t))
}

// An embedded object is trusted only from its own origin.
func TestAnnounceEmbeddedObjectFromForeignBoosterNeedsFetch(t *testing.T) {
	f := newAnnounceFixture(t)
	f.origin.status = http.StatusServiceUnavailable
	require.Equal(t, 200, f.announce(t, threadsNoteDoc()))
	require.Zero(t, f.reblogs(t), "maid.test cannot vouch for a threads.test object")
	require.Len(t, f.retries(t), 1)
}

func TestAnnounceEmbeddedObjectUsesFetchedCopy(t *testing.T) {
	f := newAnnounceFixture(t)
	doc := threadsNoteDoc()
	doc["content"] = "<p>forged</p>"
	require.Equal(t, 200, f.announce(t, doc))
	require.Equal(t, int64(1), f.reblogs(t))
	var original models.Status
	require.NoError(t, f.w.conn.Preload("Content").Where("origin_uri = ?", threadsNote).First(&original).Error)
	require.Contains(t, original.Content.Content, "hello")
}

func TestAnnounceEmbeddedObjectFromSameOrigin(t *testing.T) {
	f := newAnnounceFixture(t)
	f.origin.status = http.StatusServiceUnavailable
	selfNote := "https://maid.test/notes/self"
	require.Equal(t, 200, f.announce(t, map[string]any{
		"id": selfNote, "type": "Note", "attributedTo": f.booster.OriginId, "content": "mine",
		"to": []any{"https://www.w3.org/ns/activitystreams#Public"},
	}))
	require.Equal(t, int64(1), f.reblogs(t))
	require.Empty(t, f.retries(t))
}

// threadsActorOrigin serves a Threads-like note whose author is unknown
// locally. Unsigned GETs of anything get `unsignedStatus`; signed actor
// GETs get `actorStatus` (200 = the actor, advertised without the trailing
// slash the note uses).
type threadsActorOrigin struct {
	mu             sync.Mutex
	unsignedStatus int
	actorStatus    int
	signedActor    int
}

const threadsActorURI = "https://threads.test/ap/users/178414/"

func (o *threadsActorOrigin) serve(r *http.Request) *http.Response {
	o.mu.Lock()
	defer o.mu.Unlock()
	if r.Header.Get("Signature") == "" {
		return outboundResponse(o.unsignedStatus, "")
	}
	if strings.HasPrefix(r.URL.Path, "/ap/users/") {
		o.signedActor++
		if o.actorStatus != 200 {
			return outboundResponse(o.actorStatus, "")
		}
		key, _ := rsa.GenerateKey(rand.Reader, 1024)
		data, _ := json.Marshal(map[string]any{
			"id": strings.TrimSuffix(threadsActorURI, "/"), "type": "Person", "preferredUsername": "gihyojp",
			"inbox":     threadsActorURI + "inbox",
			"publicKey": map[string]any{"id": threadsActorURI + "#main-key", "owner": threadsActorURI, "publicKeyPem": pemPKIX(key)},
		})
		return outboundResponse(200, string(data))
	}
	doc := threadsNoteDoc()
	doc["attributedTo"] = threadsActorURI
	data, _ := json.Marshal(doc)
	return outboundResponse(200, string(data))
}

func newThreadsActorFixture(t *testing.T, actorStatus int) (*announceFixture, *threadsActorOrigin) {
	w := newRelayWorld(t)
	maid := "maid.test"
	booster := &models.Account{Username: "luna", Domain: &maid, OriginId: "https://maid.test/users/luna"}
	require.NoError(t, w.conn.Create(booster).Error)
	o := &threadsActorOrigin{unsignedStatus: http.StatusInternalServerError, actorStatus: actorStatus}
	withFetchTransport(t, func(r *http.Request) (*http.Response, error) { return o.serve(r), nil }, testSigner(t))
	return &announceFixture{w: w, booster: booster}, o
}

// Threads answers unsigned actor GETs with 500: the author is fetched signed
// and stored under the URI the note names.
func TestAnnounceOfThreadsPostResolvesAuthorSigned(t *testing.T) {
	f, o := newThreadsActorFixture(t, 200)
	require.Equal(t, 200, f.announce(t, threadsNote))
	require.Equal(t, int64(1), f.reblogs(t))
	require.Equal(t, 1, o.signedActor)
	var author models.Account
	require.NoError(t, f.w.conn.Where("origin_id = ?", threadsActorURI).First(&author).Error)
}

// An author that still fails (5xx even signed) leaves the boost queued.
func TestAnnounceAuthorServerErrorIsRetried(t *testing.T) {
	f, o := newThreadsActorFixture(t, http.StatusInternalServerError)
	require.Equal(t, 200, f.announce(t, threadsNote))
	require.Zero(t, f.reblogs(t))
	require.Len(t, f.retries(t), 1)

	o.mu.Lock()
	o.actorStatus = 200
	o.mu.Unlock()
	require.NoError(t, ProcessAnnounceRetries(context.Background(), time.Now().Add(time.Hour), 10))
	require.Equal(t, int64(1), f.reblogs(t))
	require.Empty(t, f.retries(t))
}

func TestActorIDMustMatchBeyondTrailingSlash(t *testing.T) {
	newRelayWorld(t)
	withFetchTransport(t, func(r *http.Request) (*http.Response, error) {
		data, _ := json.Marshal(map[string]any{"id": "https://threads.test/ap/users/other", "type": "Person", "inbox": "https://threads.test/inbox"})
		return outboundResponse(200, string(data)), nil
	}, testSigner(t))
	_, err := getOrCreateRemoteAccountContext(context.Background(), threadsActorURI)
	require.ErrorContains(t, err, "does not match")
}
