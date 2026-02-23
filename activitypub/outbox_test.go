package activitypub

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

func TestBuildCreateActivity(t *testing.T) {
	now := time.Now()
	status := &models.Status{
		Content:   models.StatusContent{Content: "<p>Hello world</p>", SpoilerText: ""},
		Sensitive: false,
		CreatedAt: &now,
	}
	status.Id = 42

	account := &models.Account{
		Username: "alice",
	}

	result := buildCreateActivity(status, account, "https://example.com")

	// Verify Create activity structure
	assert.Equal(t, "Create", result["type"])
	assert.Equal(t, "https://example.com/users/alice", result["actor"])
	assert.Equal(t, "https://example.com/users/alice/statuses/42#create", result["id"])

	// Verify published uses status.CreatedAt
	assert.Equal(t, &now, result["published"])

	// Verify to/cc
	to, ok := result["to"].([]string)
	assert.True(t, ok)
	assert.Contains(t, to, "https://www.w3.org/ns/activitystreams#Public")

	cc, ok := result["cc"].([]string)
	assert.True(t, ok)
	assert.Contains(t, cc, "https://example.com/users/alice/followers")

	// Verify embedded Note object
	noteObj, ok := result["object"].(map[string]interface{})
	assert.True(t, ok)
	assert.Equal(t, "Note", noteObj["type"])
	assert.Equal(t, "https://example.com/users/alice/statuses/42", noteObj["id"])
	assert.Equal(t, "https://example.com/users/alice", noteObj["attributedTo"])
	assert.Equal(t, "<p>Hello world</p>", noteObj["content"])
	assert.Equal(t, false, noteObj["sensitive"])
}

func TestBuildCreateActivityWithSpoiler(t *testing.T) {
	now := time.Now()
	status := &models.Status{
		Content:   models.StatusContent{Content: "hidden content", SpoilerText: "CW: spoiler"},
		Sensitive: true,
		CreatedAt: &now,
	}
	status.Id = 1

	account := &models.Account{Username: "bob"}
	result := buildCreateActivity(status, account, "https://example.com")

	noteObj := result["object"].(map[string]interface{})
	assert.Equal(t, true, noteObj["sensitive"])
	assert.Equal(t, "CW: spoiler", noteObj["summary"])
}

func TestBuildCreateActivityNoSpoiler(t *testing.T) {
	now := time.Now()
	status := &models.Status{
		Content:   models.StatusContent{Content: "regular post", SpoilerText: ""},
		Sensitive: false,
		CreatedAt: &now,
	}
	status.Id = 1

	account := &models.Account{Username: "alice"}
	result := buildCreateActivity(status, account, "https://example.com")

	noteObj := result["object"].(map[string]interface{})
	_, hasSummary := noteObj["summary"]
	assert.False(t, hasSummary, "summary should not be present when SpoilerText is empty")
}

func TestBuildCreateActivityPublishedTime(t *testing.T) {
	// Verify that the published time comes from status.CreatedAt, not time.Now()
	past := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	status := &models.Status{
		Content:   models.StatusContent{Content: "old post"},
		CreatedAt: &past,
	}
	status.Id = 99

	account := &models.Account{Username: "alice"}
	result := buildCreateActivity(status, account, "https://example.com")

	// Both the activity and the note should use the status creation time
	assert.Equal(t, &past, result["published"])

	noteObj := result["object"].(map[string]interface{})
	assert.Equal(t, &past, noteObj["published"])
}
