package activitypub

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoteSource(t *testing.T) {
	now := time.Now()
	note := &Note{
		ID:        "https://example.com/users/test/statuses/1",
		Type:      "Note", //nolint:goconst
		Content:   "<p>Hello world</p>",
		Published: &now,
		Source:    &Source{Content: "Hello world", MediaType: "text/plain"},
	}

	// Verify Source field is serialized
	data, err := json.Marshal(note)
	require.NoError(t, err)

	var parsed map[string]any

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	source, ok := parsed["source"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "Hello world", source["content"])
	assert.Equal(t, "text/plain", source["mediaType"])
}

func TestNoteBtoBcc(t *testing.T) {
	now := time.Now()
	note := &Note{
		ID:        "https://example.com/users/test/statuses/1",
		Type:      "Note",
		Content:   "test", //nolint:goconst
		Published: &now,
		Bto:       []string{"secret@example.com"},
		Bcc:       []string{"hidden@example.com"},
	}

	data, err := json.Marshal(note)
	require.NoError(t, err)

	var parsed map[string]any

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	// Verify Bto and Bcc are serialized
	bto, _ := parsed["bto"].([]any)
	bcc, _ := parsed["bcc"].([]any)

	assert.Len(t, bto, 1)
	assert.Len(t, bcc, 1)
}

func TestSourceJSON(t *testing.T) {
	source := &Source{
		Content:   "**bold text**",
		MediaType: "text/markdown",
	}

	data, err := json.Marshal(source)
	require.NoError(t, err)

	var parsed Source

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "**bold text**", parsed.Content)
	assert.Equal(t, "text/markdown", parsed.MediaType)
}

func TestNoteJSONRoundtrip(t *testing.T) {
	now := time.Now()
	note := &Note{
		Context:      "https://www.w3.org/ns/activitystreams", //nolint:goconst
		ID:           "https://example.com/statuses/1",
		Type:         "Note",
		Summary:      "Content warning",
		InReplyTo:    "https://example.com/statuses/0",
		Published:    &now,
		Updated:      &now,
		URL:          "https://example.com/@user/1",
		AttributedTo: "https://example.com/users/user",
		To:           []string{"https://www.w3.org/ns/activitystreams#Public"}, //nolint:goconst
		CC:           []string{"https://example.com/users/user/followers"},
		Sensitive:    true,
		Content:      "<p>Test content</p>",
		Source:       &Source{Content: "Test content", MediaType: "text/plain"},
	}

	// Test JSON roundtrip
	data, err := json.Marshal(note)
	require.NoError(t, err)

	var parsed Note

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, note.ID, parsed.ID)
	assert.Equal(t, note.Type, parsed.Type)
	assert.Equal(t, note.Summary, parsed.Summary)
	assert.Equal(t, note.InReplyTo, parsed.InReplyTo)
	assert.Equal(t, note.URL, parsed.URL)
	assert.Equal(t, note.AttributedTo, parsed.AttributedTo)
	assert.Equal(t, note.Sensitive, parsed.Sensitive)
	assert.Equal(t, note.Content, parsed.Content)
	assert.Equal(t, note.Source.Content, parsed.Source.Content)
	assert.Equal(t, note.Source.MediaType, parsed.Source.MediaType)
	assert.Equal(t, note.To, parsed.To)
	assert.Equal(t, note.CC, parsed.CC)
}

func TestActivityStructures(t *testing.T) {
	now := time.Now()

	// Test Activity
	activity := &Activity{
		Context:   "https://www.w3.org/ns/activitystreams",
		ID:        "https://example.com/activities/1",
		Type:      "Create",                         //nolint:goconst
		Actor:     "https://example.com/users/test", //nolint:goconst
		Object:    "https://example.com/statuses/1",
		To:        []string{"https://www.w3.org/ns/activitystreams#Public"},
		CC:        []string{"https://example.com/users/test/followers"},
		Published: &now,
	}

	data, err := json.Marshal(activity)
	require.NoError(t, err)

	var parsed Activity

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, activity.ID, parsed.ID)
	assert.Equal(t, activity.Type, parsed.Type)
	assert.Equal(t, activity.Actor, parsed.Actor)
}

func TestCreateActivityStructure(t *testing.T) {
	activity := &CreateActivity{
		Activity{
			ID:     "https://example.com/activities/create/1",
			Type:   "Create",
			Actor:  "https://example.com/users/test",
			Object: map[string]string{"type": "Note", "content": "test"}, //nolint:goconst
		},
	}

	assert.Equal(t, "Create", activity.Type)
	assert.Equal(t, "https://example.com/activities/create/1", activity.ID)
}

func TestAnnounceActivityStructure(t *testing.T) {
	activity := &AnnounceActivity{
		Activity{
			ID:     "https://example.com/activities/announce/1",
			Type:   "Announce", //nolint:goconst
			Actor:  "https://example.com/users/test",
			Object: "https://other.com/statuses/123",
		},
	}

	assert.Equal(t, "Announce", activity.Type)
}

func TestLikeActivityStructure(t *testing.T) {
	activity := &LikeActivity{
		Activity{
			ID:     "https://example.com/activities/like/1",
			Type:   "Like", //nolint:goconst
			Actor:  "https://example.com/users/test",
			Object: "https://other.com/statuses/456",
		},
	}

	assert.Equal(t, "Like", activity.Type)
}

func TestCollectionStructure(t *testing.T) {
	collection := &Collection{
		ID:         "https://example.com/users/test/followers",
		Type:       "OrderedCollection", //nolint:goconst
		TotalItems: 10,
		First:      "https://example.com/users/test/followers?page=1",
	}

	data, err := json.Marshal(collection)
	require.NoError(t, err)

	var parsed Collection

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, 10, parsed.TotalItems)
	assert.Equal(t, "OrderedCollection", parsed.Type)
}

func TestOrderedCollectionPageStructure(t *testing.T) {
	page := &OrderedCollectionPage{
		ID:           "https://example.com/users/test/followers?page=1",
		Type:         "OrderedCollectionPage", //nolint:goconst
		Next:         "https://example.com/users/test/followers?page=2",
		PartOf:       "https://example.com/users/test/followers",
		OrderedItems: []any{"https://example.com/users/follower1", "https://example.com/users/follower2"},
	}

	data, err := json.Marshal(page)
	require.NoError(t, err)

	var parsed OrderedCollectionPage

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Len(t, parsed.OrderedItems, 2)
}

func TestActorStructure(t *testing.T) {
	now := time.Now()
	actor := &Actor{
		Context:           []string{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"},
		ID:                "https://example.com/users/test",
		Type:              "Person", //nolint:goconst
		Following:         "https://example.com/users/test/following",
		Followers:         "https://example.com/users/test/followers",
		Inbox:             "https://example.com/users/test/inbox",
		Outbox:            "https://example.com/users/test/outbox",
		SharedInbox:       "https://example.com/inbox",
		PreferredUsername: "test",
		Name:              "Test User",
		Summary:           "A test user",
		URL:               "https://example.com/@test",
		Icon: &Image{
			Type: "Image", //nolint:goconst
			URL:  "https://example.com/avatar.png",
		},
		Image: &Image{
			Type: "Image",
			URL:  "https://example.com/header.png",
		},
		PublicKey: &PublicKey{
			ID:           "https://example.com/users/test#main-key",
			Owner:        "https://example.com/users/test",
			PublicKeyPem: "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----", //nolint:goconst
		},
		Published: &now,
	}

	data, err := json.Marshal(actor)
	require.NoError(t, err)

	var parsed Actor

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "Person", parsed.Type)
	assert.Equal(t, "test", parsed.PreferredUsername)
	assert.NotNil(t, parsed.PublicKey)
}

func TestWebFingerResponse(t *testing.T) {
	response := &WebFingerResponse{
		Subject: "acct:test@example.com",
		Aliases: []string{"https://example.com/users/test", "https://example.com/@test"},
		Links: []WebFingerLink{
			{Rel: "self", Type: "application/activity+json", Href: "https://example.com/users/test"},
			{Rel: "profile", Type: "text/html", Href: "https://example.com/@test"},
		},
	}

	data, err := json.Marshal(response)
	require.NoError(t, err)

	var parsed WebFingerResponse

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "acct:test@example.com", parsed.Subject)
	assert.Len(t, parsed.Links, 2)
}

func TestImageStructure(t *testing.T) {
	img := &Image{
		Type:      "Image",
		MediaType: "image/png",
		URL:       "https://example.com/image.png",
		Name:      "An image",
	}

	data, err := json.Marshal(img)
	require.NoError(t, err)

	var parsed Image

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "image/png", parsed.MediaType)
}

func TestAttachmentStructure(t *testing.T) {
	attachment := &Attachment{
		Type:  "PropertyValue",
		Name:  "Website",
		Value: "https://example.com",
	}

	data, err := json.Marshal(attachment)
	require.NoError(t, err)

	var parsed Attachment

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "PropertyValue", parsed.Type)
}

func TestTagStructure(t *testing.T) {
	tag := &Tag{
		Type: "Mention", //nolint:goconst
		Href: "https://example.com/users/test",
		Name: "@test@example.com", //nolint:goconst
	}

	data, err := json.Marshal(tag)
	require.NoError(t, err)

	var parsed Tag

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "Mention", parsed.Type)
	assert.Equal(t, "@test@example.com", parsed.Name)
}

func TestPublicKeyStructure(t *testing.T) {
	pk := &PublicKey{
		ID:           "https://example.com/users/test#main-key",
		Owner:        "https://example.com/users/test",
		PublicKeyPem: "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
	}

	data, err := json.Marshal(pk)
	require.NoError(t, err)

	var parsed PublicKey

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/users/test#main-key", parsed.ID)
}

func TestEndpointsStructure(t *testing.T) {
	endpoints := &Endpoints{ //nolint:gosec // G101: reviewed and safe
		SharedInbox:                "https://example.com/inbox",
		OauthAuthorizationEndpoint: "https://example.com/oauth/authorize",
		OauthTokenEndpoint:         "https://example.com/oauth/token",
	}

	data, err := json.Marshal(endpoints)
	require.NoError(t, err)

	var parsed Endpoints

	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/inbox", parsed.SharedInbox)
}

func TestRetryPolicy(t *testing.T) {
	// Test that retry times increase
	assert.Len(t, RetryPolicy, 5)
	assert.Equal(t, 5*time.Minute, RetryPolicy[0])
	assert.Equal(t, 30*time.Minute, RetryPolicy[1])
	assert.Equal(t, 2*time.Hour, RetryPolicy[2])
	assert.Equal(t, 12*time.Hour, RetryPolicy[3])
	assert.Equal(t, 24*time.Hour, RetryPolicy[4])
}

func TestGetNextRetryTime(t *testing.T) {
	// Test first retry
	nextTime := GetNextRetryTime(0)
	expectedTime := time.Now().Add(5 * time.Minute)
	assert.WithinDuration(t, expectedTime, nextTime, time.Second)

	// Test last retry index
	nextTime = GetNextRetryTime(len(RetryPolicy) - 1)
	expectedTime = time.Now().Add(24 * time.Hour)
	assert.WithinDuration(t, expectedTime, nextTime, time.Second)

	// Test beyond policy length (should use last interval)
	nextTime = GetNextRetryTime(100)
	expectedTime = time.Now().Add(24 * time.Hour)
	assert.WithinDuration(t, expectedTime, nextTime, time.Second)
}
