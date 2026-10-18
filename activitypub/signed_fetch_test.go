package activitypub

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// secureModeServer answers like Mastodon in authorized-fetch mode: unsigned
// GETs get `status`, signed ones get the document.
type secureModeServer struct {
	mu       sync.Mutex
	status   int
	unsigned int
	signed   []string // keyIds
}

func (s *secureModeServer) serve(r *http.Request) *http.Response {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("Signature") == "" {
		s.unsigned++
		return outboundResponse(s.status, "")
	}
	sig, err := ParseSignatureHeader(r.Header.Get("Signature"))
	if err != nil {
		return outboundResponse(400, "")
	}
	s.signed = append(s.signed, sig.KeyID)
	data, _ := json.Marshal(map[string]any{"id": "https://secure.test" + r.URL.Path, "type": "Person", "inbox": "https://secure.test/inbox"})
	return outboundResponse(200, string(data))
}

func withFetchTransport(t *testing.T, rt outboundTransport, signer func(context.Context) (*Client, error)) {
	t.Helper()
	oldClient, oldSigner := sharedHTTPClient, systemSigningClient
	t.Cleanup(func() { sharedHTTPClient, systemSigningClient = oldClient, oldSigner })
	sharedHTTPClient = &http.Client{Transport: rt}
	systemSigningClient = signer
}

func testSigner(t *testing.T) func(context.Context) (*Client, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return func(context.Context) (*Client, error) { return NewClient(key, "https://local.test/actor"), nil }
}

func TestFetchClientRetriesSignedOnAuthorizedFetch(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusGone, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			srv := &secureModeServer{status: status}
			withFetchTransport(t, func(r *http.Request) (*http.Response, error) { return srv.serve(r), nil }, testSigner(t))

			actor, err := NewFetchClient().FetchActor(context.Background(), "https://secure.test/users/alice")
			require.NoError(t, err)
			require.Equal(t, "https://secure.test/users/alice", actor.ID)
			require.Equal(t, 1, srv.unsigned)
			require.Equal(t, []string{"https://local.test/actor#main-key"}, srv.signed)
		})
	}
}

func TestFetchClientDoesNotSignOtherFailures(t *testing.T) {
	srv := &secureModeServer{status: http.StatusTeapot}
	withFetchTransport(t, func(r *http.Request) (*http.Response, error) { return srv.serve(r), nil }, testSigner(t))

	_, status, err := NewFetchClient().getWithStatus(context.Background(), "https://secure.test/users/gone")
	require.Error(t, err)
	require.Equal(t, http.StatusTeapot, status)
	require.Equal(t, http.StatusTeapot, fetchStatusCode(err))
	require.Equal(t, "unexpected status code: 418", err.Error())
	require.Empty(t, srv.signed)
}

func TestFetchClientSignedRetryStillRefused(t *testing.T) {
	calls := 0
	withFetchTransport(t, func(r *http.Request) (*http.Response, error) {
		calls++
		return outboundResponse(http.StatusForbidden, ""), nil
	}, testSigner(t))

	_, status, err := NewFetchClient().getWithStatus(context.Background(), "https://blocked.test/users/x")
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, 2, calls, "exactly one signed retry")
	require.False(t, isTransientFetchError(err))
}

func TestFetchClientWithoutSignerReturnsOriginalError(t *testing.T) {
	srv := &secureModeServer{status: http.StatusUnauthorized}
	withFetchTransport(t, func(r *http.Request) (*http.Response, error) { return srv.serve(r), nil },
		func(context.Context) (*Client, error) { return nil, errors.New("no proxy key") })

	_, err := NewFetchClient().Get(context.Background(), "https://secure.test/users/alice")
	require.Error(t, err)
	require.Equal(t, http.StatusUnauthorized, fetchStatusCode(err))
	require.Empty(t, srv.signed)
}

func TestSignedClientIsNotRetried(t *testing.T) {
	srv := &secureModeServer{status: http.StatusUnauthorized}
	withFetchTransport(t, func(r *http.Request) (*http.Response, error) { return srv.serve(r), nil },
		func(context.Context) (*Client, error) { t.Fatal("signed clients have no fallback"); return nil, nil })
	signer, _ := testSigner(t)(context.Background())
	_, err := signer.Get(context.Background(), "https://secure.test/users/alice")
	require.NoError(t, err)
	require.Zero(t, srv.unsigned)
	require.Len(t, srv.signed, 1)
}

func TestRelayFetchUsesSignedRetry(t *testing.T) {
	srv := &secureModeServer{status: http.StatusUnauthorized}
	withFetchTransport(t, func(r *http.Request) (*http.Response, error) { return srv.serve(r), nil }, testSigner(t))
	obj, status, err := relayFetch(context.Background(), "https://secure.test/users/alice")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "https://secure.test/users/alice", obj["id"])
}

func TestIsTransientFetchError(t *testing.T) {
	cases := map[string]struct {
		err  error
		want bool
	}{
		"404":      {&FetchError{StatusCode: 404}, false},
		"410":      {&FetchError{StatusCode: 410}, false},
		"401":      {&FetchError{StatusCode: 401}, false},
		"429":      {&FetchError{StatusCode: 429}, true},
		"502":      {&FetchError{StatusCode: 502}, true},
		"wrapped":  {fmt.Errorf("x: %w", &FetchError{StatusCode: 503}), true},
		"timeout":  {context.DeadlineExceeded, true},
		"refused":  {fmt.Errorf("fetching resource: %w", &net.OpError{Op: "dial", Err: errors.New("connection refused")}), true},
		"nxdomain": {&net.DNSError{Err: "no such host", IsNotFound: true}, false},
		"dns-tmp":  {&net.DNSError{Err: "server misbehaving", IsTemporary: true}, true},
		"parse":    {errors.New("parsing actor: invalid character"), false},
		"nil":      {nil, false},
	}
	for name, tc := range cases {
		require.Equal(t, tc.want, isTransientFetchError(tc.err), name)
	}
}

// Every remote ActivityPub GET must go through NewFetchClient (or a client
// signing for a specific local actor). NewClient(nil, ...) has no signed
// fallback and breaks against authorized-fetch servers.
func TestNoUnsignedFetchClientsInProductionCode(t *testing.T) {
	pattern := regexp.MustCompile(`NewClient\(nil,`)
	for _, dir := range []string{".", "../datatransfer", "../controllers", "../cmd"} {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for i, line := range strings.Split(string(data), "\n") {
				trimmed := strings.TrimSpace(line)
				if pattern.MatchString(line) && !strings.HasPrefix(trimmed, "//") && !(path == "client.go" && strings.Contains(line, "client := NewClient(nil")) {
					t.Errorf("%s:%d uses an unsigned fetch client: %s", path, i+1, trimmed)
				}
			}
			return nil
		})
		require.NoError(t, err)
	}
}

// --- inbox behaviour against authorized-fetch origins ---

// secureOrigin wraps fakeOrigin: unsigned GETs are refused with 401, and
// actorStatus (when set) replaces alice's actor document.
type secureOrigin struct {
	*fakeOrigin
	actorStatus int
	unsigned    int
	signedBy    []string
}

func (o *secureOrigin) serve(r *http.Request) *http.Response {
	if r.Header.Get("Signature") == "" {
		o.unsigned++
		return outboundResponse(401, `{"error":"Request not signed"}`)
	}
	sig, _ := ParseSignatureHeader(r.Header.Get("Signature"))
	o.signedBy = append(o.signedBy, sig.KeyID)
	if o.actorStatus != 0 && r.URL.Path == "/users/alice" {
		return outboundResponse(o.actorStatus, "")
	}
	return o.fakeOrigin.serve(r)
}

func newSecureRelayWorld(t *testing.T) (*relayWorld, *secureOrigin, *models.Relay) {
	w := newRelayWorld(t)
	relay := w.subscribe(t)
	require.Equal(t, models.RelayStateAccepted, relay.State)
	secure := &secureOrigin{fakeOrigin: w.origin}
	base := sharedHTTPClient.Transport
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "origin.test" {
			return secure.serve(r), nil
		}
		return base.RoundTrip(r)
	})}
	return w, secure, relay
}

// postStatus delivers activity signed by client and returns the exact HTTP
// status (relayWorld.post folds every 2xx into 200).
func (w *relayWorld) postStatus(t *testing.T, client *Client, inbox string, activity map[string]any) int {
	t.Helper()
	body, err := json.Marshal(activity)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, inbox, bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/activity+json")
	require.NoError(t, client.signRequest(req, body))
	resp := serveVia(w.engine, req)
	_ = resp.Body.Close()
	return resp.StatusCode
}

func forwardedCreate(n string) map[string]any {
	return map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams", "id": e2eOrigin + "/users/alice/statuses/" + n + "/activity",
		"type": "Create", "actor": e2eOrigin + "/users/alice", "to": []string{asPublicURI},
		"object": originNote(n, "embedded"),
	}
}

// Regression for c.hime.at: relayed posts from secure-mode instances failed
// with "Failed to get/create remote account: unexpected status code: 401"
// and HTTP 500, so relays redelivered forever.
func TestRelayedCreateFromAuthorizedFetchOrigin(t *testing.T) {
	w, secure, _ := newSecureRelayWorld(t)
	w.origin.notes["/users/alice/statuses/20"] = originNote("20", "<p>from secure mode</p>")

	require.Equal(t, 200, w.post(t, w.relay.client(), e2eBase+"/inbox", forwardedCreate("20")))
	var status models.Status
	require.NoError(t, w.conn.Preload("Content").Preload("Account").Where("origin_uri = ?", e2eOrigin+"/users/alice/statuses/20").First(&status).Error)
	require.Contains(t, status.Content.Content, "from secure mode")
	require.Equal(t, e2eOrigin+"/users/alice", status.Account.OriginId)
	require.Positive(t, secure.unsigned)
	require.NotEmpty(t, secure.signedBy)
	for _, keyID := range secure.signedBy {
		require.True(t, strings.HasPrefix(keyID, e2eBase+"/"), "signed as a local system actor, got %s", keyID)
	}
}

func TestRelayedCreateWithUnresolvableAuthorIsNotRetried(t *testing.T) {
	w, secure, _ := newSecureRelayWorld(t)
	secure.actorStatus = http.StatusGone
	w.origin.notes["/users/alice/statuses/21"] = originNote("21", "orphan")

	require.Equal(t, 202, w.postStatus(t, w.relay.client(), e2eBase+"/inbox", forwardedCreate("21")))
	var count int64
	require.NoError(t, w.conn.Model(&models.Status{}).Where("origin_uri = ?", e2eOrigin+"/users/alice/statuses/21").Count(&count).Error)
	require.Zero(t, count)
	// The delivery is settled: a redelivery is deduplicated.
	require.Equal(t, 200, w.post(t, w.relay.client(), e2eBase+"/inbox", forwardedCreate("21")))
}

func TestRelayedCreateWithTemporarilyFailingAuthorIsRetryable(t *testing.T) {
	w, secure, _ := newSecureRelayWorld(t)
	secure.actorStatus = http.StatusBadGateway
	w.origin.notes["/users/alice/statuses/22"] = originNote("22", "later")

	require.Equal(t, 503, w.postStatus(t, w.relay.client(), e2eBase+"/inbox", forwardedCreate("22")))
	var processed int64
	require.NoError(t, w.conn.Model(&ProcessedActivity{}).Where("activity_id = ?", e2eOrigin+"/users/alice/statuses/22/activity").Count(&processed).Error)
	require.Zero(t, processed, "a transient failure must stay retryable")

	secure.actorStatus = 0
	require.Equal(t, 200, w.post(t, w.relay.client(), e2eBase+"/inbox", forwardedCreate("22")))
	require.NoError(t, w.conn.Model(&models.Status{}).Where("origin_uri = ?", e2eOrigin+"/users/alice/statuses/22").Count(&processed).Error)
	require.EqualValues(t, 1, processed)
}

// Relays forward Update{Person}; it has no attributedTo and used to be
// refused with 403 "object origin does not match its author".
func TestRelayedActorUpdateIsIgnoredNot403(t *testing.T) {
	w := newRelayWorld(t)
	w.subscribe(t)
	update := map[string]any{"id": e2eOrigin + "/users/alice#updates/1", "type": "Update", "actor": e2eOrigin + "/users/alice",
		"to": []string{asPublicURI}, "object": map[string]any{"id": e2eOrigin + "/users/alice", "type": "Person"}}
	require.Equal(t, 202, w.postStatus(t, w.relay.client(), e2eBase+"/inbox", update))
}

// attributedTo may be an array with one actor (or an embedded actor); that
// used to be refused with 403 "actor mismatch".
func TestRelayedNoteWithArrayAttributedTo(t *testing.T) {
	w := newRelayWorld(t)
	w.subscribe(t)
	note := originNote("23", "array author")
	note["attributedTo"] = []any{map[string]any{"id": e2eOrigin + "/users/alice", "type": "Person"}}
	w.origin.notes["/users/alice/statuses/23"] = note
	require.Equal(t, 200, w.post(t, w.relay.client(), e2eBase+"/inbox", forwardedCreate("23")))
	var count int64
	require.NoError(t, w.conn.Model(&models.Status{}).Where("origin_uri = ?", e2eOrigin+"/users/alice/statuses/23").Count(&count).Error)
	require.EqualValues(t, 1, count)
}

// A boost of an object whose attributedTo names an actor on another host
// must not create a status for that actor.
func TestAnnounceOfSpoofedObjectIsNotImported(t *testing.T) {
	w := newRelayWorld(t)
	w.subscribe(t)
	w.origin.notes["/users/alice/statuses/30"] = map[string]any{"id": e2eOrigin + "/users/alice/statuses/30", "type": "Note",
		"attributedTo": "https://victim.test/users/bob", "content": "spoof", "to": []string{asPublicURI}}
	announce := map[string]any{"id": e2eOrigin + "/users/alice/statuses/30/activity", "type": "Announce", "actor": e2eOrigin + "/users/alice",
		"to": []string{asPublicURI}, "object": e2eOrigin + "/users/alice/statuses/30"}
	require.Equal(t, 200, w.postStatus(t, NewClient(e2eOrigKey, e2eOrigin+"/users/alice"), e2eBase+"/inbox", announce))
	var count int64
	require.NoError(t, w.conn.Model(&models.Status{}).Count(&count).Error)
	require.Zero(t, count)
}
