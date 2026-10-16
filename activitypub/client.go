package activitypub

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/moderation"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// sharedHTTPClient is a package-level shared HTTP client with proper connection
// pooling and SSRF protection. All Client instances reuse this to avoid creating
// short-lived connections and to ensure no outbound requests reach internal/private
// addresses.
var sharedHTTPClient = ssrfSafeHTTPClient

// Client is an ActivityPub HTTP client.
type Client struct {
	httpClient *http.Client
	privateKey *rsa.PrivateKey
	actorID    string
	keyID      string
	// signedFallback lazily provides a signing client. An unsigned GET that is
	// rejected with 401/403 (Mastodon "authorized fetch" / secure mode, GoToSocial,
	// Misskey with signed-fetch enforcement) is retried once with an HTTP
	// signature from that client.
	signedFallback func(context.Context) (*Client, error)
}

// NewClient creates a new ActivityPub client.
func NewClient(privateKey *rsa.PrivateKey, actorID string) *Client {
	return &Client{
		httpClient: sharedHTTPClient,
		privateKey: privateKey,
		actorID:    actorID,
		keyID:      actorID + "#main-key",
	}
}

// Get fetches a remote ActivityPub resource. Requests are signed when the
// client has a key; unsigned clients created by NewFetchClient retry once with
// a signature when the remote requires authorized fetch.
func (c *Client) Get(ctx context.Context, uri string) ([]byte, error) {
	data, _, err := c.getWithStatus(ctx, uri)
	return data, err
}

// getWithStatus is Get that also reports the final HTTP status (0 when no
// response was received).
func (c *Client) getWithStatus(ctx context.Context, uri string) ([]byte, int, error) {
	data, status, err := c.get(ctx, uri)
	if err != nil && requiresSignedFetch(status) && c.privateKey == nil && c.signedFallback != nil {
		signer, signerErr := c.signedFallback(ctx)
		if signerErr != nil || signer == nil || signer.privateKey == nil {
			if signerErr != nil {
				log.Warn("Signed fetch of %s unavailable: %v", uri, signerErr)
			}
			return nil, status, err
		}
		data, status, err = signer.get(ctx, uri)
	}
	return data, status, err
}

// requiresSignedFetch reports whether an unsigned GET was refused because
// the remote enforces authorized fetch (Mastodon secure mode answers 401,
// some servers answer 403).
func requiresSignedFetch(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

// FetchError is a non-200 answer to a federation GET.
type FetchError struct {
	StatusCode int
}

func (e *FetchError) Error() string {
	return fmt.Sprintf("unexpected status code: %d", e.StatusCode)
}

// fetchStatusCode returns the HTTP status of a failed fetch, or 0.
func fetchStatusCode(err error) int {
	var fetchErr *FetchError
	if errors.As(err, &fetchErr) {
		return fetchErr.StatusCode
	}
	return 0
}

// isTransientFetchError reports whether a failed federation fetch is worth
// retrying later: timeouts, connection failures, rate limiting and 5xx.
// Everything else (404/410, 401/403 even with a signature, malformed or
// mismatched documents, SSRF rejections) will fail the same way again.
func isTransientFetchError(err error) bool {
	if err == nil {
		return false
	}
	if status := fetchStatusCode(err); status != 0 {
		switch status {
		case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
			return true
		}
		return status >= 500
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr) && urlErr.Timeout()
}

func (c *Client) get(ctx context.Context, uri string) ([]byte, int, error) {
	if err := validateFederationURI(uri); err != nil {
		return nil, 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("creating request: %w", err)
	}

	// Set ActivityPub headers. The profile-qualified ld+json form is the one
	// the ActivityPub spec mandates; Misskey and Mastodon accept either.
	req.Header.Set("Accept", `application/activity+json, application/ld+json; profile="https://www.w3.org/ns/activitystreams"`)
	req.Header.Set("User-Agent", "Community/1.0 ActivityPub Client")

	if c.privateKey != nil {
		if err := c.signGetRequest(req); err != nil {
			return nil, 0, fmt.Errorf("signing request: %w", err)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("fetching resource: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, &FetchError{StatusCode: resp.StatusCode}
	}

	// Limit response body size to prevent OOM attacks
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	return data, resp.StatusCode, err
}

// signGetRequest adds a draft-cavage HTTP signature over
// "(request-target) host date", which is what Mastodon, Misskey, Pleroma and
// GoToSocial require for authorized fetch.
func (c *Client) signGetRequest(req *http.Request) error {
	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	signingString := fmt.Sprintf("(request-target): get %s\nhost: %s\ndate: %s", signRequestPath(req.URL), host, date)
	signature, err := SignWithRSA(c.privateKey, []byte(signingString))
	if err != nil {
		return err
	}
	req.Header.Set("Signature", fmt.Sprintf(`keyId="%s",algorithm="rsa-sha256",headers="(request-target) host date",signature="%s"`,
		c.signingKeyID(), base64.StdEncoding.EncodeToString(signature)))
	return nil
}

// Post sends an activity to a remote inbox with HTTP signature.
func (c *Client) Post(ctx context.Context, inboxURL string, activity map[string]any) error {
	if err := validateFederationURI(inboxURL); err != nil {
		return err
	}
	if u, err := url.Parse(inboxURL); err == nil && !moderation.DeliveryAllowed(u.Host) {
		return ErrDeliveryBlocked
	}

	body, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("marshaling activity: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, inboxURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/activity+json")
	req.Header.Set("User-Agent", "Community/1.0 ActivityPub Client")

	// Sign the request
	if c.privateKey != nil {
		err := c.signRequest(req, body)
		if err != nil {
			return fmt.Errorf("signing request: %w", err)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting activity: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))

		return fmt.Errorf("remote returned error: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// signRequest signs an HTTP request with HTTP Signatures.
func (c *Client) signRequest(req *http.Request, body []byte) error {
	// Parse inbox URL to get host
	u, err := url.Parse(req.URL.String())
	if err != nil {
		return err
	}

	// Build the signing string
	date := time.Now().UTC().Format(http.TimeFormat)
	req.Header.Set("Date", date)

	// Create digest for the body
	hash := sha256.Sum256(body)
	digest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])
	req.Header.Set("Digest", digest)

	// Build the signature string
	// Per HTTP Signatures spec: (request-target) value is lowercased method + path
	// Host should use the actual host value, not lowercased
	signingString := fmt.Sprintf("(request-target): post %s\nhost: %s\ndate: %s\ndigest: %s",
		signRequestPath(u),
		u.Host,
		date,
		digest,
	)

	// Sign with RSA-SHA256
	signature, err := SignWithRSA(c.privateKey, []byte(signingString))
	if err != nil {
		return fmt.Errorf("signing: %w", err)
	}

	// Build Signature header
	sigHeader := fmt.Sprintf(`keyId="%s",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="%s"`,
		c.signingKeyID(),
		base64.StdEncoding.EncodeToString(signature),
	)

	req.Header.Set("Signature", sigHeader)

	return nil
}

// FetchActor fetches a remote actor by URI.
func (c *Client) FetchActor(ctx context.Context, actorURI string) (*Actor, error) {
	data, err := c.Get(ctx, actorURI)
	if err != nil {
		return nil, err
	}

	var actor Actor
	if err := json.Unmarshal(data, &actor); err != nil {
		return nil, fmt.Errorf("parsing actor: %w", err)
	}

	return &actor, nil
}

// FetchObject fetches a remote object by URI.
func (c *Client) FetchObject(ctx context.Context, objectURI string) (map[string]any, error) {
	data, err := c.Get(ctx, objectURI)
	if err != nil {
		return nil, err
	}

	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("parsing object: %w", err)
	}

	return obj, nil
}

// FetchStatus fetches a remote status by URI and creates a local copy.
func (c *Client) FetchStatus(ctx context.Context, statusURI string) (*Note, error) {
	data, err := c.Get(ctx, statusURI)
	if err != nil {
		return nil, err
	}

	var note Note
	if err := json.Unmarshal(data, &note); err != nil {
		return nil, fmt.Errorf("parsing status: %w", err)
	}

	return &note, nil
}

// ErrDeliveryBlocked is returned for requests to a blocked host or deliveries
// to a manually suspended instance. Such deliveries are dropped, not retried.
var ErrDeliveryBlocked = errors.New("federation with this host is blocked by the instance moderation settings")

// validateFederationURI ensures the URI uses http/https scheme with a non-empty
// host before any outbound federation request. The SSRF-safe transport handles
// IP filtering, but we validate the scheme here as an additional defense layer.
func validateFederationURI(uri string) error {
	if uri == "" {
		return fmt.Errorf("empty URI")
	}
	u, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("invalid URI %q: %w", uri, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("non-HTTP scheme %q in federation URI %q", u.Scheme, uri)
	}
	if u.Host == "" {
		return fmt.Errorf("empty host in federation URI %q", uri)
	}
	// Misskey isFederationAllowedHost: no requests to blocked hosts or
	// their subdomains.
	if moderation.Current().IsBlockedHost(u.Hostname()) {
		return ErrDeliveryBlocked
	}
	return validateLabURL(u)
}

// signRequestPath returns the request-target path including query string,
// matching the HTTP Signatures draft spec.
func signRequestPath(u *url.URL) string {
	path := u.Path
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return path
}

func (c *Client) signingKeyID() string {
	if c.keyID != "" {
		return c.keyID
	}
	return c.actorID + "#main-key"
}

// NewFetchClient returns an unsigned client for fetching remote objects that
// transparently retries with the instance's system proxy actor signature when
// the remote server enforces authorized fetch. Every remote ActivityPub GET
// (actors, objects, keys, collections) that is not already made on behalf of
// a specific local actor must use it: NewClient(nil, "") has no fallback.
func NewFetchClient() *Client {
	client := NewClient(nil, "")
	client.signedFallback = systemSigningClient
	return client
}

// systemSignerTTL bounds how long the parsed system signing key is reused,
// so a rotated key is picked up without a restart.
const systemSignerTTL = 10 * time.Minute

var systemSignerCache struct {
	sync.Mutex
	client  *Client
	expires time.Time
}

func resetSystemSigner() {
	systemSignerCache.Lock()
	systemSignerCache.client = nil
	systemSignerCache.Unlock()
}

// systemSigningClient returns a client signing as the instance's system
// proxy actor (users/system.proxy, the instance-wide fetch key). It is a
// variable so tests can substitute a signer.
//
// Signed fetches also run inside inbox transactions (resolving the author of
// an incoming post), so the key is looked up on the transaction's own
// connection: a second connection would deadlock SQLite (one connection) and
// could exhaust the pool elsewhere. The parsed client is cached, but only
// when it was read outside a transaction that might still roll back.
var systemSigningClient = func(ctx context.Context) (*Client, error) {
	systemSignerCache.Lock()
	if cached := systemSignerCache.client; cached != nil && time.Now().Before(systemSignerCache.expires) {
		systemSignerCache.Unlock()
		return cached, nil
	}
	systemSignerCache.Unlock()

	conn := activityDB(ctx)
	service := &ProxyService{db: conn}
	account, _, err := service.GetOrCreateProxyAccount()
	if err != nil {
		return nil, err
	}
	actorURI := service.getProxyActorURI()
	key, err := localAccountKeyWith(conn, account)
	if err != nil {
		return nil, err
	}
	private, err := models.ParseAccountPrivateKey(key.PrivateKey)
	if err != nil {
		return nil, err
	}
	client := NewClient(private, actorURI)
	client.keyID = key.SigningKeyID(actorURI)
	if _, inTransaction := ctx.Value(inboxTransactionKey{}).(*gorm.DB); !inTransaction {
		systemSignerCache.Lock()
		systemSignerCache.client, systemSignerCache.expires = client, time.Now().Add(systemSignerTTL)
		systemSignerCache.Unlock()
	}
	return client, nil
}
