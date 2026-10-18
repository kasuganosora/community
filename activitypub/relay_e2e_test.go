package activitypub

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

const (
	e2eBase     = "https://c.hime.test"
	e2eRelay    = "https://relay.test"
	e2eOrigin   = "https://origin.test"
	asPublicURI = "https://www.w3.org/ns/activitystreams#Public"
)

var (
	e2eKeysOnce             sync.Once
	e2eRelayKey, e2eOrigKey *rsa.PrivateKey
)

func e2eKeys(t *testing.T) {
	e2eKeysOnce.Do(func() {
		var err error
		e2eRelayKey, err = rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		e2eOrigKey, err = rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
	})
}

// relayDB has every table the inbox/outbox paths touch. On SQLite the
// auto-increment ids are made real INTEGER PRIMARY KEYs so accounts created
// by the code (instance actor, remote authors) get ids like production.
func relayDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	if conn.Dialector.Name() == "sqlite" {
		sqlDB.SetMaxOpenConns(1)
	}
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous); _ = sqlDB.Close() })
	all := []any{&models.User{}, &models.Account{}, &models.AccountExtra{}, &models.AccountKey{}, &models.AccountFederationKey{}, &models.RemoteActorIdentity{},
		&models.Emoji{}, &models.AccountEmoji{}, &models.Status{}, &models.StatusContent{}, &models.StatusToAccount{}, &models.Tag{}, &models.StatusTag{},
		&models.MediaAttachment{}, &models.Follow{}, &models.Block{}, &models.SystemSetting{}, &models.Poll{}, &models.PollOption{}, &models.StatusEmoji{},
		&models.Application{}, &models.Conversation{}, &models.AccountConversation{}, &models.Notification{}, &models.FederationOutbox{}, &Delivery{},
		&ProcessedActivity{}, &AnnounceRetry{}, &InboxJob{}, &models.Relay{}, &models.DomainBlock{}}
	if conn.Dialector.Name() == "sqlite" {
		for _, m := range all {
			stmt := &gorm.Statement{DB: conn}
			require.NoError(t, stmt.Parse(m))
			for _, field := range stmt.Schema.Fields {
				if field.AutoIncrement {
					field.DataType = schema.Int
				}
			}
		}
	}
	require.NoError(t, conn.AutoMigrate(all...))

	prevURL, prevConfigured := config.C.AppBaseURL, config.AppBaseURLConfigured()
	config.SetAppBaseURL(e2eBase, true)
	t.Cleanup(func() { config.SetAppBaseURL(prevURL, prevConfigured) })
	resetInstanceActorCache()
	resetRelayActorCache()
	resetProxyCache()
	t.Cleanup(func() { resetInstanceActorCache(); resetRelayActorCache(); resetProxyCache() })
	return conn
}

func e2eEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/actor", ActorProfile)
	r.GET("/users/:username", ActorProfile)
	r.POST("/actor/inbox", Inbox)
	r.POST("/users/:username/inbox", Inbox)
	r.POST("/inbox", SharedInbox)
	r.GET("/.well-known/webfinger", WebFinger)
	return r
}

func serveVia(h http.Handler, r *http.Request) *http.Response {
	if r.Body != nil {
		data, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(data))
	}
	r.Host = r.URL.Host
	r.RequestURI = r.URL.RequestURI()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Result()
}

func pemPKIX(key *rsa.PrivateKey) string {
	der, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// fakeRelay behaves like yukimochi/Activity-Relay (4 of the 5 target relays):
// it verifies HTTP signatures by fetching the signer's actor, accepts a
// Follow of as:Public that is not addressed to as:Public, stores the
// follower's endpoints.sharedInbox and relays public Create/Update/Delete
// from subscribed hosts.
type fakeRelay struct {
	t           *testing.T
	mu          sync.Mutex
	subscribers map[string]string // host -> shared inbox
	pending     []map[string]any  // Accepts to send: {"inbox":..., "activity":...}
	relayed     []map[string]any
	ignored     []map[string]any
	undone      []string
	followers   []string
}

func (f *fakeRelay) actor() map[string]any {
	return map[string]any{
		"@context": []string{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
		"id":       e2eRelay + "/actor", "type": "Service", "preferredUsername": "relay",
		"inbox": e2eRelay + "/inbox",
		"publicKey": map[string]any{"id": e2eRelay + "/actor#main-key", "owner": e2eRelay + "/actor",
			// Activity-Relay publishes PKCS#1 ("RSA PUBLIC KEY").
			"publicKeyPem": string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&e2eRelayKey.PublicKey)}))},
	}
}

func contains(v any, s string) bool {
	switch x := v.(type) {
	case string:
		return x == s
	case []any:
		for _, e := range x {
			if e == s {
				return true
			}
		}
	}
	return false
}

func (f *fakeRelay) serve(r *http.Request) *http.Response {
	t := f.t
	if r.Method == http.MethodGet && r.URL.Path == "/actor" {
		data, _ := json.Marshal(f.actor())
		return outboundResponse(200, string(data))
	}
	if r.Method != http.MethodPost || r.URL.Path != "/inbox" {
		return outboundResponse(404, "")
	}
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))
	sig, err := ParseSignatureHeader(r.Header.Get("Signature"))
	if err != nil {
		return outboundResponse(401, "no signature")
	}
	sum := sha256.Sum256(body)
	if r.Header.Get("Digest") != "SHA-256="+base64.StdEncoding.EncodeToString(sum[:]) {
		return outboundResponse(400, "digest mismatch")
	}
	// Fetch the key owner like the real relay does.
	actorURI, _, _ := strings.Cut(sig.KeyID, "#")
	resp, err := sharedHTTPClient.Get(actorURI)
	if err != nil || resp.StatusCode != 200 {
		return outboundResponse(401, "cannot fetch signer")
	}
	var signer Actor
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&signer))
	require.NotNil(t, signer.PublicKey)
	pub, err := ParsePublicKey(signer.PublicKey.PublicKeyPem)
	require.NoError(t, err)
	if err := VerifyRequest(r, pub); err != nil {
		return outboundResponse(401, "bad signature: "+err.Error())
	}
	var activity map[string]any
	require.NoError(t, json.Unmarshal(body, &activity))
	f.mu.Lock()
	defer f.mu.Unlock()
	host := (func() string { u, _ := url.Parse(signer.ID); return u.Host })()
	if contains(activity["to"], asPublicURI) || contains(activity["cc"], asPublicURI) {
		switch activity["type"] {
		case "Create", "Update", "Delete", "Move":
			if _, ok := f.subscribers[host]; !ok {
				return outboundResponse(401, "follow in advance")
			}
			f.relayed = append(f.relayed, activity)
		default:
			f.ignored = append(f.ignored, activity)
		}
		return outboundResponse(202, "")
	}
	switch activity["type"] {
	case "Follow":
		if activity["object"] == asPublicURI {
			require.NotNil(t, signer.Endpoints, "Mastodon-style subscribers need endpoints.sharedInbox")
			f.subscribers[host] = signer.Endpoints.SharedInbox
			f.pending = append(f.pending, map[string]any{"inbox": signer.Inbox, "activity": map[string]any{
				"@context": "https://www.w3.org/ns/activitystreams", "id": e2eRelay + "/actor/activities/accept-1",
				"type": "Accept", "actor": e2eRelay + "/actor", "object": activity, "to": []string{activity["actor"].(string)},
			}})
		} else if activity["object"] == e2eRelay+"/actor" && signer.Type == "Application" {
			f.followers = append(f.followers, signer.ID)
		}
	case "Undo":
		inner, _ := activity["object"].(map[string]any)
		if inner["type"] == "Follow" {
			delete(f.subscribers, host)
			f.undone = append(f.undone, inner["id"].(string))
		}
	}
	return outboundResponse(202, "")
}

func (f *fakeRelay) client() *Client {
	return NewClient(e2eRelayKey, e2eRelay+"/actor")
}

// flush sends the queued Accepts, signed by the relay.
func (f *fakeRelay) flush(t *testing.T) {
	f.mu.Lock()
	pending := f.pending
	f.pending = nil
	f.mu.Unlock()
	for _, p := range pending {
		require.NoError(t, f.client().Post(context.Background(), p["inbox"].(string), p["activity"].(map[string]any)))
	}
}

// origin is a Mastodon instance whose posts the relay forwards to us.
type fakeOrigin struct {
	mu    sync.Mutex
	notes map[string]map[string]any // path -> object; nil value = 410 Gone
}

func (o *fakeOrigin) serve(r *http.Request) *http.Response {
	o.mu.Lock()
	defer o.mu.Unlock()
	if r.URL.Path == "/users/alice" {
		data, _ := json.Marshal(map[string]any{
			"@context": []string{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
			"id":       e2eOrigin + "/users/alice", "type": "Person", "preferredUsername": "alice",
			"inbox": e2eOrigin + "/users/alice/inbox", "followers": e2eOrigin + "/users/alice/followers",
			"publicKey": map[string]any{"id": e2eOrigin + "/users/alice#main-key", "owner": e2eOrigin + "/users/alice", "publicKeyPem": pemPKIX(e2eOrigKey)},
		})
		return outboundResponse(200, string(data))
	}
	note, ok := o.notes[r.URL.Path]
	if !ok {
		return outboundResponse(404, "")
	}
	if note == nil {
		return outboundResponse(410, "")
	}
	data, _ := json.Marshal(note)
	return outboundResponse(200, string(data))
}

func originNote(n string, content string, to ...string) map[string]any {
	if len(to) == 0 {
		to = []string{asPublicURI}
	}
	return map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       e2eOrigin + "/users/alice/statuses/" + n, "type": "Note",
		"attributedTo": e2eOrigin + "/users/alice", "content": content,
		"published": "2026-10-09T01:00:00Z", "to": to, "cc": []string{e2eOrigin + "/users/alice/followers"},
	}
}

type relayWorld struct {
	conn   *gorm.DB
	engine *gin.Engine
	relay  *fakeRelay
	origin *fakeOrigin
}

func newRelayWorld(t *testing.T) *relayWorld {
	e2eKeys(t)
	w := &relayWorld{conn: relayDB(t), engine: e2eEngine(), relay: &fakeRelay{t: t, subscribers: map[string]string{}}, origin: &fakeOrigin{notes: map[string]map[string]any{}}}
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "c.hime.test":
			return serveVia(w.engine, r), nil
		case "relay.test":
			return w.relay.serve(r), nil
		case "origin.test":
			return w.origin.serve(r), nil
		}
		return outboundResponse(404, ""), nil
	})}
	return w
}

func (w *relayWorld) post(t *testing.T, client *Client, inbox string, activity map[string]any) int {
	t.Helper()
	err := client.Post(context.Background(), inbox, activity)
	if err == nil {
		return 200
	}
	var code int
	_, scanErr := fmt.Sscanf(err.Error()[strings.Index(err.Error(), "remote returned error: "):], "remote returned error: %d", &code)
	require.NoError(t, scanErr, err.Error())
	return code
}

func itoa(i int) string { return strconv.Itoa(i) }

func (w *relayWorld) subscribe(t *testing.T) *models.Relay {
	t.Helper()
	_, _, err := EnsureInstanceActor(context.Background())
	require.NoError(t, err)
	svc := NewRelayService()
	relay, err := svc.CreateRelay(e2eRelay + "/inbox")
	require.NoError(t, err)
	require.NoError(t, svc.EnableRelay(relay.ID))
	queue := NewDeliveryQueue(nil, "", 0)
	queue.processPending()
	var d Delivery
	require.NoError(t, w.conn.Where("target_inbox = ?", e2eRelay+"/inbox").First(&d).Error)
	require.Equal(t, DeliveryStatusDelivered, d.Status, d.LastError)
	w.relay.flush(t)
	got, err := svc.GetRelayByID(relay.ID)
	require.NoError(t, err)
	return got
}

func TestRelaySubscriptionEndToEnd(t *testing.T) {
	w := newRelayWorld(t)
	relay := w.subscribe(t)
	require.Equal(t, models.RelayStateAccepted, relay.State, "the relay's Accept must settle the subscription")
	require.Equal(t, e2eBase+"/inbox", w.relay.subscribers["c.hime.test"])

	// The relay actor is not a cached account and none was created for it.
	var relayAccounts int64
	require.NoError(t, w.conn.Model(&models.Account{}).Where("origin_id = ?", e2eRelay+"/actor").Count(&relayAccounts).Error)
	require.Zero(t, relayAccounts)

	// Outbound: only the PUBLIC local post reaches the relay.
	author := &models.Account{Username: "writer"}
	require.NoError(t, w.conn.Create(author).Error)
	for _, v := range []uint8{models.StatusVisibilityPublic, models.StatusVisibilityUnlisted, models.StatusVisibilityPrivate, models.StatusVisibilityDirect} {
		require.NoError(t, w.conn.Create(&models.Status{AccountId: author.Id, Visibility: v, Content: models.StatusContent{Content: "visibility " + itoa(int(v))}}).Error)
	}
	(&OutboundWorker{}).processPending()
	queue := NewDeliveryQueue(nil, "", 0)
	queue.processPending()
	queue.processPending()
	require.Len(t, w.relay.relayed, 1, "ignored: %v", w.relay.ignored)
	require.Equal(t, "Create", w.relay.relayed[0]["type"])
	require.Equal(t, e2eBase+"/users/writer", w.relay.relayed[0]["actor"])

	// Inbound: the relay forwards alice's Create with tampered embedded content.
	w.origin.notes["/users/alice/statuses/1"] = originNote("1", "<p>genuine</p>")
	forwarded := map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams", "id": e2eOrigin + "/users/alice/statuses/1/activity",
		"type": "Create", "actor": e2eOrigin + "/users/alice", "to": []string{asPublicURI},
		"object": originNote("1", "<p>EVIL embedded copy</p>"),
	}
	require.Equal(t, 200, w.post(t, w.relay.client(), e2eBase+"/inbox", forwarded))
	var status models.Status
	require.NoError(t, w.conn.Preload("Content").Preload("Account").Where("origin_uri = ?", e2eOrigin+"/users/alice/statuses/1").First(&status).Error)
	require.Contains(t, status.Content.Content, "genuine")
	require.NotContains(t, status.Content.Content, "EVIL")
	require.EqualValues(t, models.StatusVisibilityPublic, status.Visibility)
	require.Equal(t, e2eOrigin+"/users/alice", status.Account.OriginId)

	// A relay Announce (LitePub style) imports the note only: no boost row,
	// no counters, no notifications.
	w.origin.notes["/users/alice/statuses/2"] = originNote("2", "<p>announced</p>")
	announce := map[string]any{"@context": "https://www.w3.org/ns/activitystreams", "id": e2eRelay + "/actor/activities/announce-2",
		"type": "Announce", "actor": e2eRelay + "/actor", "to": []string{e2eRelay + "/actor/followers"}, "object": e2eOrigin + "/users/alice/statuses/2"}
	require.Equal(t, 200, w.post(t, w.relay.client(), e2eBase+"/actor/inbox", announce))
	var announced models.Status
	require.NoError(t, w.conn.Where("origin_uri = ?", e2eOrigin+"/users/alice/statuses/2").First(&announced).Error)
	require.Zero(t, announced.ReblogsCount)
	var boosts, notifications int64
	require.NoError(t, w.conn.Model(&models.Status{}).Where("reblog_id IS NOT NULL").Count(&boosts).Error)
	require.NoError(t, w.conn.Model(&models.Notification{}).Count(&notifications).Error)
	require.Zero(t, boosts)
	require.Zero(t, notifications)

	// A forwarded Create whose object lives on another host than its author is refused.
	w.origin.notes["/users/alice/statuses/3"] = map[string]any{"id": e2eOrigin + "/users/alice/statuses/3", "type": "Note",
		"attributedTo": "https://victim.test/users/bob", "content": "spoof", "to": []string{asPublicURI}}
	spoof := map[string]any{"id": "https://victim.test/users/bob/statuses/3/activity", "type": "Create", "actor": "https://victim.test/users/bob",
		"to": []string{asPublicURI}, "object": e2eOrigin + "/users/alice/statuses/3"}
	require.Equal(t, 403, w.post(t, w.relay.client(), e2eBase+"/inbox", spoof))

	// A non-public object is never imported through a relay.
	w.origin.notes["/users/alice/statuses/4"] = originNote("4", "followers only", e2eOrigin+"/users/alice/followers")
	private := map[string]any{"id": e2eOrigin + "/users/alice/statuses/4/activity", "type": "Create", "actor": e2eOrigin + "/users/alice",
		"to": []string{asPublicURI}, "object": originNote("4", "followers only")}
	require.Equal(t, 200, w.post(t, w.relay.client(), e2eBase+"/inbox", private))
	var count int64
	require.NoError(t, w.conn.Model(&models.Status{}).Where("origin_uri = ?", e2eOrigin+"/users/alice/statuses/4").Count(&count).Error)
	require.Zero(t, count)

	// A forwarded Delete is ignored while the origin still serves the note,
	// and applied once the origin answers 410 Gone.
	del := map[string]any{"id": e2eOrigin + "/users/alice/statuses/1#delete", "type": "Delete", "actor": e2eOrigin + "/users/alice",
		"to": []string{asPublicURI}, "object": map[string]any{"id": e2eOrigin + "/users/alice/statuses/1", "type": "Tombstone"}}
	require.Equal(t, 200, w.post(t, w.relay.client(), e2eBase+"/inbox", del))
	require.NoError(t, w.conn.Model(&models.Status{}).Where("origin_uri = ?", e2eOrigin+"/users/alice/statuses/1").Count(&count).Error)
	require.EqualValues(t, 1, count)
	w.origin.mu.Lock()
	w.origin.notes["/users/alice/statuses/1"] = nil
	w.origin.mu.Unlock()
	del["id"] = e2eOrigin + "/users/alice/statuses/1#delete-2"
	require.Equal(t, 200, w.post(t, w.relay.client(), e2eBase+"/inbox", del))
	require.NoError(t, w.conn.Model(&models.Status{}).Where("origin_uri = ?", e2eOrigin+"/users/alice/statuses/1").Count(&count).Error)
	require.Zero(t, count)

	// Unsubscribe: Undo Follow without "to", and nothing more is relayed.
	require.NoError(t, NewRelayService().DisableRelay(relay.ID))
	queue.processPending()
	require.Equal(t, []string{relay.FollowActivityID}, w.relay.undone)
	require.Empty(t, w.relay.subscribers)
	require.NoError(t, w.conn.Create(&models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "after"}}).Error)
	(&OutboundWorker{}).processPending()
	queue.processPending()
	require.Len(t, w.relay.relayed, 1)

	// Forwarding from a relay that is no longer accepted is rejected.
	forwarded["id"] = e2eOrigin + "/users/alice/statuses/2/activity"
	forwarded["object"] = originNote("2", "x")
	require.Equal(t, 401, w.post(t, w.relay.client(), e2eBase+"/inbox", forwarded))
}

func TestRelayAcceptRequiresTheRelayActor(t *testing.T) {
	w := newRelayWorld(t)
	_, _, err := EnsureInstanceActor(context.Background())
	require.NoError(t, err)
	svc := NewRelayService()
	relay, err := svc.CreateRelay(e2eRelay + "/inbox")
	require.NoError(t, err)
	require.NoError(t, svc.EnableRelay(relay.ID))
	relay, err = svc.GetRelayByID(relay.ID)
	require.NoError(t, err)

	// Another server (alice@origin.test) cannot accept on the relay's behalf.
	alice := NewClient(e2eOrigKey, e2eOrigin+"/users/alice")
	accept := map[string]any{"id": e2eOrigin + "/accept/1", "type": "Accept", "actor": e2eOrigin + "/users/alice",
		"object": map[string]any{"id": relay.FollowActivityID, "type": "Follow", "actor": InstanceActorURI(), "object": asPublicURI}}
	require.Equal(t, 403, w.post(t, alice, e2eBase+"/actor/inbox", accept))
	got, _ := svc.GetRelayByID(relay.ID)
	require.Equal(t, models.RelayStatePending, got.State)

	// The relay's Reject (object by id only) is honoured.
	reject := map[string]any{"id": e2eRelay + "/actor/activities/reject-1", "type": "Reject", "actor": e2eRelay + "/actor", "object": relay.FollowActivityID}
	require.Equal(t, 200, w.post(t, w.relay.client(), e2eBase+"/inbox", reject))
	got, _ = svc.GetRelayByID(relay.ID)
	require.Equal(t, models.RelayStateRejected, got.State)
}

// A boost by a remote user (not a relay) still creates a reblog.
func TestNonRelayAnnounceStillBoosts(t *testing.T) {
	w := newRelayWorld(t)
	w.subscribe(t)
	w.origin.notes["/users/alice/statuses/9"] = originNote("9", "boost me")
	announce := map[string]any{"id": e2eOrigin + "/users/alice/statuses/9/activity", "type": "Announce", "actor": e2eOrigin + "/users/alice",
		"to": []string{asPublicURI}, "object": e2eOrigin + "/users/alice/statuses/9"}
	require.Equal(t, 200, w.post(t, NewClient(e2eOrigKey, e2eOrigin+"/users/alice"), e2eBase+"/inbox", announce))
	var boosts int64
	require.NoError(t, w.conn.Model(&models.Status{}).Where("reblog_id IS NOT NULL").Count(&boosts).Error)
	require.EqualValues(t, 1, boosts)
}
