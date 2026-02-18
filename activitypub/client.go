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
	"strings"
	"time"
)

// Client is an ActivityPub HTTP client
type Client struct {
	httpClient *http.Client
	privateKey *rsa.PrivateKey
	actorID    string
}

// NewClient creates a new ActivityPub client
func NewClient(privateKey *rsa.PrivateKey, actorID string) *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		privateKey: privateKey,
		actorID:    actorID,
	}
}

// Get fetches a remote ActivityPub resource
func (c *Client) Get(ctx context.Context, uri string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", uri, nil)
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// Post sends an activity to a remote inbox with HTTP signature
func (c *Client) Post(ctx context.Context, inboxURL string, activity map[string]interface{}) error {
	body, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("marshaling activity: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", inboxURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/activity+json")
	req.Header.Set("User-Agent", "Community/1.0 ActivityPub Client")

	// Sign the request
	if c.privateKey != nil {
		if err := c.signRequest(req, body); err != nil {
			return fmt.Errorf("signing request: %w", err)
		}
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting activity: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("remote returned error: %d - %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// signRequest signs an HTTP request with HTTP Signatures
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
	signingString := fmt.Sprintf("(request-target): post %s\nhost: %s\ndate: %s\ndigest: %s",
		strings.ToLower(u.Path),
		strings.ToLower(u.Host),
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

// FetchActor fetches a remote actor by URI
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

// FetchObject fetches a remote object by URI
func (c *Client) FetchObject(ctx context.Context, objectURI string) (map[string]interface{}, error) {
	data, err := c.Get(ctx, objectURI)
	if err != nil {
		return nil, err
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("parsing object: %w", err)
	}

	return obj, nil
}
