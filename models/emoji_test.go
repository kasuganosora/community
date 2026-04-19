package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// Emoji
// ============================================================================

func TestEmoji_TableName(t *testing.T) {
	e := Emoji{}
	assert.Equal(t, "emojis", e.TableName())
}

func TestEmoji_Fields(t *testing.T) {
	e := Emoji{
		Id:              1,
		Shortcode:       "party",
		Url:             "https://example.com/emoji/party.png",
		StaticUrl:       "https://example.com/emoji/party_static.png",
		VisibleInPicker: true,
		Domain:          "",
		Category:        "fun",
	}

	assert.Equal(t, uint64(1), e.Id)
	assert.Equal(t, "party", e.Shortcode)
	assert.Equal(t, "https://example.com/emoji/party.png", e.Url)
	assert.True(t, e.VisibleInPicker)
	assert.Equal(t, "fun", e.Category)
}

func TestEmojis_GetIds_Empty(t *testing.T) {
	emojis := Emojis{}
	ids := emojis.GetIds()
	assert.Empty(t, ids)
}

func TestEmojis_GetIds(t *testing.T) {
	emojis := Emojis{
		{Id: 1, Shortcode: "a"},
		{Id: 5, Shortcode: "b"},
		{Id: 10, Shortcode: "c"},
	}
	ids := emojis.GetIds()
	assert.Equal(t, []uint64{1, 5, 10}, ids)
}

// ============================================================================
// Emoji.MatchEmojiTags
// ============================================================================

func TestMatchEmojiTags_NoEmojis(t *testing.T) {
	text := "Hello world, no emojis here"
	result := MatchEmojiTags(text)
	assert.Empty(t, result)
}

func TestMatchEmojiTags_SingleMatch(t *testing.T) {
	text := "I feel :happy: today"
	result := MatchEmojiTags(text)
	assert.Len(t, result, 1)
	assert.Contains(t, result, ":happy:")
}

func TestMatchEmojiTags_MultipleMatches(t *testing.T) {
	text := ":happy: :sad: :happy:"
	result := MatchEmojiTags(text)
	assert.Len(t, result, 2)
	assert.Contains(t, result, ":happy:")
	assert.Contains(t, result, ":sad:")
}

func TestMatchEmojiTags_PartialMatch(t *testing.T) {
	text := ":happiness: and :sad:"
	result := MatchEmojiTags(text)
	// Both :happiness: and :sad: match the pattern :[a-zA-Z0-9_]+:
	assert.Len(t, result, 2)
	assert.Contains(t, result, ":happiness:")
	assert.Contains(t, result, ":sad:")
}

func TestMatchEmojiTags_CaseSensitivity(t *testing.T) {
	text := ":HAPPY: :Happy: :happy:"
	result := MatchEmojiTags(text)
	// Regex is case-sensitive by default, so all 3 are different
	assert.Len(t, result, 3)
}

func TestMatchEmojiTags_EmptyText(t *testing.T) {
	text := ""
	result := MatchEmojiTags(text)
	assert.Empty(t, result)
}

func TestMatchEmojiTags_SpecialChars(t *testing.T) {
	text := "text with :test_emoji: and normal text"
	result := MatchEmojiTags(text)
	assert.Len(t, result, 1)
	assert.Contains(t, result, ":test_emoji:")
}
