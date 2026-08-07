package activitypub

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
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
}

// NewClient creates a new ActivityPub client.
func NewClient(privateKey *rsa.PrivateKey, actorID string) *Client {
	return &Client{
		httpClient: sharedHTTPClient,
		privateKey: privateKey,
		actorID:    actorID,
	}
}

// Get fetches a remote ActivityPub resource.
func (c *Client) Get(ctx context.Context, uri string) ([]byte, error) {
	if err := validateFederationURI(uri); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	// Set ActivityPub headers
	req.Header.Set("Accept", "application/activity+json, application/ld+json")
	req.Header.Set("User-Agent", "Community/1.0 ActivityPub Client")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching resource: %w", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Limit response body size to prevent OOM attacks
	return io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
}

// Post sends an activity to a remote inbox with HTTP signature.
func (c *Client) Post(ctx context.Context, inboxURL string, activity map[string]any) error {
	if err := validateFederationURI(inboxURL); err != nil {
		return err
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
	sigHeader := fmt.Sprintf(`keyId="%s#main-key",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="%s"`,
		c.actorID,
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
	return nil
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
