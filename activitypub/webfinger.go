package activitypub

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/systemsettings"
)

// WebFinger handles GET /.well-known/webfinger
func WebFinger(c *gin.Context) {
	resource := c.Query("resource")
	if resource == "" {
		c.JSON(400, gin.H{"error": "resource parameter required"})
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
	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")

	// Build response
	baseURL := "https://" + domain
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

// HostMeta handles GET /.well-known/host-meta
func HostMeta(c *gin.Context) {
	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	baseURL := "https://" + domain

	xml := `<?xml version="1.0" encoding="UTF-8"?>
<XRD xmlns="http://docs.oasis-open.org/ns/xri/xrd-1.0">
  <Link rel="lrdd" type="application/xrd+xml" template="` + baseURL + `/.well-known/webfinger?resource={uri}"/>
</XRD>`

	c.Data(200, "application/xrd+xml", []byte(xml))
}

// NodeInfo handles GET /.well-known/nodeinfo
func NodeInfo(c *gin.Context) {
	domain := systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	baseURL := "https://" + domain

	response := map[string]interface{}{
		"links": []map[string]string{
			{
				"rel":  "http://nodeinfo.diaspora.software/ns/schema/2.0",
				"href": baseURL + "/nodeinfo/2.0",
			},
		},
	}

	c.JSON(200, response)
}

// httpClient is a shared HTTP client with timeout
var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

// DiscoverActor discovers an actor via WebFinger
func DiscoverActor(acct string) (*Actor, error) {
	// Parse acct format (user@domain or acct:user@domain)
	acct = strings.TrimPrefix(acct, "acct:")
	parts := strings.Split(acct, "@")
	if len(parts) != 2 {
		return nil, ErrInvalidAcctFormat
	}

	// username := parts[0] // unused for now
	domain := parts[1]

	// Query WebFinger endpoint
	webfingerURL := "https://" + domain + "/.well-known/webfinger?resource=acct:" + acct

	resp, err := httpClient.Get(webfingerURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, ErrWebFingerFailed
	}

	var webfinger WebFingerResponse
	if err := json.NewDecoder(resp.Body).Decode(&webfinger); err != nil {
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

	// Fetch the actor
	client := NewClient(nil, "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return client.FetchActor(ctx, actorURL)
}

// Error definitions
var (
	ErrInvalidAcctFormat  = &ActivityPubError{Message: "invalid acct format"}
	ErrWebFingerFailed    = &ActivityPubError{Message: "webfinger discovery failed"}
	ErrActorNotFound      = &ActivityPubError{Message: "actor not found"}
)

// ActivityPubError represents an ActivityPub error
type ActivityPubError struct {
	Message string
}

func (e *ActivityPubError) Error() string {
	return e.Message
}


