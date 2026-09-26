package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// WebFinger handles GET /.well-known/webfinger.
func WebFinger(c *gin.Context) {
	resource := c.Query("resource")
	if resource == "" {
		c.JSON(400, gin.H{"error": "resource parameter required"}) //nolint:goconst

		return
	}

	// Parse the resource (should be acct:username@domain)
	if !strings.HasPrefix(resource, "acct:") {
		c.JSON(400, gin.H{"error": "invalid resource format"})

		return
	}

	// Extract username
	acct := strings.TrimPrefix(resource, "acct:")

	parts := strings.Split(acct, "@")
	if len(parts) != 2 {
		c.JSON(400, gin.H{"error": "invalid acct format"})

		return
	}

	username := parts[0]
	domain := systemsettings.GetDomain()

	// Verify the requested domain matches ours
	if parts[1] != domain {
		c.JSON(404, gin.H{"error": "user not found"}) //nolint:goconst

		return
	}

	// Verify the user exists in our database
	var account models.Account
	err := db.GetDB().Where("username = ? AND domain IS NULL", username).First(&account).Error
	if err != nil {
		c.JSON(404, gin.H{"error": "user not found"})

		return
	}

	// Build response
	baseURL := systemsettings.GetBaseURL()
	actorURL := baseURL + "/users/" + username

	response := WebFingerResponse{
		Subject: resource,
		Aliases: []string{
			actorURL,
			baseURL + "/@" + username,
		},
		Links: []WebFingerLink{
			{
				Rel:  "self",
				Type: "application/activity+json",
				Href: actorURL,
			},
			{
				Rel:  "http://webfinger.net/rel/profile-page",
				Type: "text/html",
				Href: baseURL + "/@" + username,
			},
			{
				Rel:  "http://ostatus.org/schema/1.0/subscribe",
				Href: baseURL + "/authorize_interaction?uri={uri}",
			},
		},
	}

	c.JSON(200, response)
}

// HostMeta handles GET /.well-known/host-meta.
func HostMeta(c *gin.Context) {
	baseURL := systemsettings.GetBaseURL()

	xml := `<?xml version="1.0" encoding="UTF-8"?>
<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0">
  <Link rel="lrdd" type="application/xrd+xml" template="` + baseURL + `/.well-known/webfinger?resource={uri}"/>
</XRD>`

	c.Data(200, "application/xrd+xml", []byte(xml))
}

// NodeInfo handles GET /.well-known/nodeinfo.
func NodeInfo(c *gin.Context) {
	baseURL := systemsettings.GetBaseURL()

	response := map[string]any{
		"links": []map[string]string{
			{
				"rel":  "http://nodeinfo.diaspora.software/ns/schema/2.0",
				"href": baseURL + "/nodeinfo/2.0", //nolint:goconst
			},
		},
	}

	c.JSON(200, response)
}

// DiscoverActor discovers an actor via WebFinger.
func DiscoverActor(acct string) (*Actor, error) {
	// Parse acct format (user@domain or acct:user@domain)
	acct = strings.TrimPrefix(acct, "acct:")

	parts := strings.Split(acct, "@")
	if len(parts) != 2 {
		return nil, ErrInvalidAcctFormat
	}

	// username := parts[0] // unused for now
	domain := parts[1]

	// Validate the domain doesn't resolve to internal addresses
	webfingerURL := "https://" + domain + "/.well-known/webfinger?resource=acct:" + acct
	if err := validateFederationURI(webfingerURL); err != nil {
		return nil, fmt.Errorf("invalid webfinger target: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, webfingerURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := ssrfSafeHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, ErrWebFingerFailed
	}

	var webfinger WebFingerResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseSize)).Decode(&webfinger); err != nil {
		return nil, err
	}

	// Find the ActivityPub actor URL
	var actorURL string

	for _, link := range webfinger.Links {
		if link.Rel == "self" && (link.Type == "application/activity+json" || link.Type == "application/ld+json") {
			actorURL = link.Href

			break
		}
	}

	if actorURL == "" {
		return nil, ErrActorNotFound
	}

	// Fetch the actor using the SSRF-safe client
	client := NewClient(nil, "")

	return client.FetchActor(ctx, actorURL)
}

// Error definitions.
var (
	ErrInvalidAcctFormat = &ActivityPubError{Message: "invalid acct format"}
	ErrWebFingerFailed   = &ActivityPubError{Message: "webfinger discovery failed"}
	ErrActorNotFound     = &ActivityPubError{Message: "actor not found"}
)

// ActivityPubError represents an ActivityPub error.
type ActivityPubError struct {
	Message string
}

func (e *ActivityPubError) Error() string {
	return e.Message
}
