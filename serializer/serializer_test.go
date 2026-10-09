package serializer

import (
	"encoding/json"
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/stretchr/testify/require"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
)

// --- Emoji tests ---

func TestToEmoji(t *testing.T) {
	emoji := &models.Emoji{
		Shortcode:       "smile",
		Url:             "https://example.com/smile.png",
		StaticUrl:       "https://example.com/smile_static.png",
		VisibleInPicker: true,
		Category:        "people",
	}

	result := ToEmoji(emoji)
	assert.Equal(t, "smile", result.Shortcode)
	assert.Equal(t, mediaproxy.URL(mediaproxy.KindEmoji, "https://example.com/smile.png"), result.Url)
	assert.Equal(t, mediaproxy.URL(mediaproxy.KindEmoji, "https://example.com/smile_static.png"), result.StaticUrl)
	assert.True(t, result.VisibleInPicker)
	assert.Equal(t, "people", result.Category)
}

func TestToEmojis(t *testing.T) {
	emojis := []*models.Emoji{
		{Shortcode: "smile", Url: "url1", StaticUrl: "static1"},
		{Shortcode: "heart", Url: "url2", StaticUrl: "static2"},
	}

	result := ToEmojis(emojis)
	assert.Len(t, result, 2)
	assert.Equal(t, "smile", result[0].Shortcode)
	assert.Equal(t, "heart", result[1].Shortcode)
}

func TestToEmojis_Empty(t *testing.T) {
	result := ToEmojis([]*models.Emoji{})
	assert.Empty(t, result)
}

// --- Rule tests ---

func TestToRule(t *testing.T) {
	rule := &models.Rule{Id: 42, Text: "Be nice to others"}
	result := ToRule(rule)
	assert.Equal(t, "42", result.Id)
	assert.Equal(t, "Be nice to others", result.Text)
}

func TestToRules(t *testing.T) {
	rules := []*models.Rule{
		{Id: 1, Text: "Rule 1"},
		{Id: 2, Text: "Rule 2"},
		{Id: 3, Text: "Rule 3"},
	}

	result := ToRules(rules)
	assert.Len(t, result, 3)
	assert.Equal(t, "1", result[0].Id)
	assert.Equal(t, "3", result[2].Id)
}

func TestToRules_Empty(t *testing.T) {
	result := ToRules([]*models.Rule{})
	assert.Empty(t, result)
}

// --- Field tests ---

func TestToFields(t *testing.T) {
	now := time.Now()
	fields := []models.AccountField{
		{Name: "Website", Val: "https://example.com", VerifiedAt: &now},
		{Name: "Location", Val: "Tokyo", VerifiedAt: nil},
	}

	result := ToFields(fields)
	assert.Len(t, result, 2)
	assert.Equal(t, "Website", result[0].Name)
	assert.Equal(t, "https://example.com", result[0].Value)
	assert.NotNil(t, result[0].VerifiedAt)
	assert.Equal(t, "Location", result[1].Name)
	assert.Nil(t, result[1].VerifiedAt)
}

func TestToFields_Empty(t *testing.T) {
	result := ToFields([]models.AccountField{})
	assert.Empty(t, result)
}

// --- Filter tests ---

func TestToFilter(t *testing.T) {
	expires := time.Now().Add(24 * time.Hour)
	filter := &models.Filter{
		Id:           10,
		Title:        "Block spam",
		Context:      []string{"home", "public"}, //nolint:goconst
		ExpiresAt:    &expires,
		FilterAction: models.FilterActionHide,
		Keywords: []*models.FilterKeyword{
			{Uuid: "abc", Keyword: "spam", WholeWord: true},
			{Uuid: "def", Keyword: "scam", WholeWord: false},
		},
	}

	result := ToFilter(filter)
	assert.Equal(t, "10", result.Id)
	assert.Equal(t, "Block spam", result.Title)
	assert.Equal(t, []string{"home", "public"}, result.Context)
	assert.Equal(t, "hide", result.FilterAction)
	assert.Len(t, result.Keywords, 2)
	assert.Equal(t, "10-abc", result.Keywords[0].Id)
	assert.Equal(t, "spam", result.Keywords[0].Keyword)
	assert.True(t, result.Keywords[0].WholeWord)
}

func TestToFilterKeywords_Nil(t *testing.T) {
	filter := &models.Filter{Id: 1, Keywords: nil}
	result := ToFilterKeywords(filter)
	assert.Empty(t, result)
}

// --- Attachment tests ---
// Note: ToAttachment/ToAttachments depend on systemsettings.GetString which requires DB.
// Only nil/empty tests can run without DB.

func TestToAttachment_Nil(t *testing.T) {
	result := ToAttachment(nil)
	assert.Nil(t, result)
}

func TestToAttachments_Empty(t *testing.T) {
	result := ToAttachments([]*models.Attachment{})
	assert.Empty(t, result)
}

// --- Poll tests ---

func TestToBasePoll(t *testing.T) {
	expires := time.Now().Add(-1 * time.Hour) // already expired
	poll := &models.Poll{
		Id:        100,
		Multiple:  true,
		ExpiresAt: &expires,
	}

	result := ToBasePoll(poll, true, 50, 20)
	assert.Equal(t, "100", result.Id)
	assert.True(t, result.Multiple)
	assert.True(t, result.Expired)
	assert.True(t, result.Voted)
	assert.Equal(t, uint64(50), result.VotesCount)
	assert.Equal(t, uint64(20), result.VotersCount)
	assert.NotNil(t, result.Options)
	assert.NotNil(t, result.OwnVotes)
	assert.NotNil(t, result.Emojis)
}

func TestToBasePoll_NotExpired(t *testing.T) {
	expires := time.Now().Add(24 * time.Hour) // future
	poll := &models.Poll{
		Id:        200,
		Multiple:  false,
		ExpiresAt: &expires,
	}

	result := ToBasePoll(poll, false, 0, 0)
	assert.Equal(t, "200", result.Id)
	assert.False(t, result.Multiple)
	assert.False(t, result.Expired)
	assert.False(t, result.Voted)
}

// --- Tag tests ---
// Note: ToTag/ToTagWithHistory depend on systemsettings.GetString which requires DB.
// Only nil/empty tests can run without DB.

func TestToTags_Nil(t *testing.T) {
	result := ToTags(nil)
	assert.NotNil(t, result)
	assert.Empty(t, result)
}

func TestToTag_Nil(t *testing.T) {
	result := ToTag(nil)
	assert.Nil(t, result)
}

func TestToTagWithHistory_Nil(t *testing.T) {
	result := ToTagWithHistory(nil)
	assert.Nil(t, result)
}

func TestToAccount_MastodonStrictShape(t *testing.T) {
	created := time.Date(2026, 10, 9, 0, 16, 37, 131129000, time.UTC)
	raw, err := json.Marshal(ToAccount(&models.Account{Id: 1, Username: "luna", CreatedAt: &created, LastStatusAt: &created}))
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(raw, &m))

	assert.Equal(t, "2026-10-09T00:00:00.000Z", m["created_at"])
	assert.Equal(t, "2026-10-09", m["last_status_at"])
	assert.Regexp(t, `^https?://.+/default-assets/avatar\.png$`, m["avatar"])
	assert.Regexp(t, `^https?://.+/default-assets/header\.png$`, m["header"])
	assert.NotContains(t, m, "avatar_decoration_id")
	assert.Equal(t, []any{}, m["roles"])
	assert.Equal(t, []any{}, m["fields"])
	assert.Equal(t, []any{}, m["emojis"])
}
