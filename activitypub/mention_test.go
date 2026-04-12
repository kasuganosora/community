package activitypub

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMentions(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		tags        []interface{}
		expectCount int
		expectURIs  []string
	}{
		{
			name:        "empty content",
			content:     "",
			tags:        nil,
			expectCount: 0,
		},
		{
			name:        "plain text mention",
			content:     "Hello @user@example.com how are you?",
			tags:        nil,
			expectCount: 1,
		},
		{
			name:        "multiple plain text mentions",
			content:     "Hello @user1@example.com and @user2@another.com",
			tags:        nil,
			expectCount: 2,
		},
		{
			name:        "local mention - no domain means no URI",
			content:     "Hey @localuser",
			tags:        nil,
			expectCount: 0, // local mentions without domain don't have URI
		},
		{
			name:    "tag array mentions",
			content: "Hello",
			tags: []interface{}{
				map[string]interface{}{
					"type": "Mention",
					"href": "https://example.com/users/test",
					"name": "@test@example.com",
				},
			},
			expectCount: 1,
			expectURIs:  []string{"https://example.com/users/test"},
		},
		{
			name: "mixed tags",
			tags: []interface{}{
				map[string]interface{}{
					"type": "Mention",
					"href": "https://example.com/users/alice",
					"name": "@alice@example.com",
				},
				map[string]interface{}{
					"type": "Hashtag",
					"name": "#test",
				},
				map[string]interface{}{
					"type": "Mention",
					"href": "https://other.com/users/bob",
					"name": "@bob@other.com",
				},
			},
			expectCount: 2,
			expectURIs:  []string{"https://example.com/users/alice", "https://other.com/users/bob"},
		},
		{
			name:        "HTML link mentions",
			content:     `<p>Hello <a href="https://example.com/users/test" class="mention">@test</a></p>`,
			tags:        nil,
			expectCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mentions := ParseMentions(tt.content, tt.tags)
			assert.Len(t, mentions, tt.expectCount)

			if tt.expectURIs != nil {
				uriMap := make(map[string]bool)
				for _, m := range mentions {
					uriMap[m.URI] = true
				}
				for _, uri := range tt.expectURIs {
					assert.True(t, uriMap[uri], "expected URI %s not found", uri)
				}
			}
		})
	}
}

func TestParseTextMentions(t *testing.T) {
	tests := []struct {
		name         string
		content      string
		expectCount  int
		checkMention func(t *testing.T, mentions []Mention)
	}{
		{
			name:        "no mentions",
			content:     "Just some text without mentions",
			expectCount: 0,
		},
		{
			name:        "single remote mention",
			content:     "Hello @user@example.com",
			expectCount: 1,
			checkMention: func(t *testing.T, mentions []Mention) {
				assert.Equal(t, "user", mentions[0].Username)
				assert.Equal(t, "example.com", mentions[0].Domain)
				assert.Equal(t, "user@example.com", mentions[0].Acct)
			},
		},
		{
			name:        "single local mention - has username but empty domain",
			content:     "Hey @localuser",
			expectCount: 1,
			checkMention: func(t *testing.T, mentions []Mention) {
				assert.Equal(t, "localuser", mentions[0].Username)
				assert.Equal(t, "", mentions[0].Domain)
				assert.Equal(t, "localuser", mentions[0].Acct)
			},
		},
		{
			name:        "multiple mentions",
			content:     "Hello @alice@social.com and @bob@network.org",
			expectCount: 2,
		},
		{
			name:        "duplicate mentions",
			content:     "@user@example.com @user@example.com",
			expectCount: 1, // Duplicates should be de-duplicated
		},
		{
			name:        "mention at end",
			content:     "Check this out @user@example.com",
			expectCount: 1,
		},
		{
			name:        "mention at start",
			content:     "@user@example.com check this out",
			expectCount: 1,
		},
		{
			name:        "email not mention - regex still matches",
			content:     "Email me at user@example.com",
			expectCount: 1, // Regex matches @user@example.com pattern
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mentions := parseTextMentions(tt.content)
			assert.Len(t, mentions, tt.expectCount)
			if tt.checkMention != nil {
				tt.checkMention(t, mentions)
			}
		})
	}
}

func TestParseHTMLMentions(t *testing.T) {
	tests := []struct {
		name    string
		content string
		expect  []Mention
	}{
		{
			name:    "no HTML links",
			content: "Just plain text",
			expect:  nil,
		},
		{
			name:    "mention link",
			content: `<a href="https://example.com/users/test">@test</a>`,
			expect: []Mention{
				{Username: "test", Domain: "example.com", URI: "https://example.com/users/test"},
			},
		},
		{
			name:    "non-mention link",
			content: `<a href="https://example.com/about">About</a>`,
			expect:  nil,
		},
		{
			name:    "multiple links",
			content: `<a href="https://example.com/users/alice">@alice</a> and <a href="https://other.com/users/bob">@bob</a>`,
			expect: []Mention{
				{Username: "alice", Domain: "example.com", URI: "https://example.com/users/alice"},
				{Username: "bob", Domain: "other.com", URI: "https://other.com/users/bob"},
			},
		},
		{
			name:    "link with @ prefix in text",
			content: `<a href="https://mastodon.social/@gargron" class="mention">@gargron@mastodon.social</a>`,
			expect: []Mention{
				{Username: "gargron", Domain: "mastodon.social", URI: "https://mastodon.social/@gargron"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mentions := parseHTMLMentions(tt.content)
			if tt.expect == nil {
				assert.Len(t, mentions, 0)
			} else {
				assert.Len(t, mentions, len(tt.expect))
				for i, expected := range tt.expect {
					if i < len(mentions) {
						assert.Equal(t, expected.Username, mentions[i].Username)
						assert.Equal(t, expected.Domain, mentions[i].Domain)
					}
				}
			}
		})
	}
}

func TestParseTagMentions(t *testing.T) {
	tests := []struct {
		name string
		tags []interface{}
		want []Mention
	}{
		{
			name: "nil tags",
			tags: nil,
			want: nil,
		},
		{
			name: "empty tags",
			tags: []interface{}{},
			want: nil,
		},
		{
			name: "single mention tag",
			tags: []interface{}{
				map[string]interface{}{
					"type": "Mention",
					"href": "https://example.com/users/test",
					"name": "@test@example.com",
				},
			},
			want: []Mention{
				{Username: "test", Domain: "example.com", URI: "https://example.com/users/test", Acct: "test@example.com"},
			},
		},
		{
			name: "mixed tag types",
			tags: []interface{}{
				map[string]interface{}{
					"type": "Hashtag",
					"name": "#test",
				},
				map[string]interface{}{
					"type": "Mention",
					"href": "https://example.com/users/alice",
					"name": "@alice@example.com",
				},
				map[string]interface{}{
					"type": "Emoji",
					"name": ":smile:",
				},
			},
			want: []Mention{
				{Username: "alice", Domain: "example.com", URI: "https://example.com/users/alice", Acct: "alice@example.com"},
			},
		},
		{
			name: "missing href",
			tags: []interface{}{
				map[string]interface{}{
					"type": "Mention",
					"name": "@test",
				},
			},
			want: nil,
		},
		{
			name: "invalid tag type",
			tags: []interface{}{
				"invalid",
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTagMentions(tt.tags)
			if tt.want == nil {
				assert.Len(t, got, 0)
			} else {
				assert.Len(t, got, len(tt.want))
				for i, expected := range tt.want {
					if i < len(got) {
						assert.Equal(t, expected.URI, got[i].URI)
						assert.Equal(t, expected.Username, got[i].Username)
						assert.Equal(t, expected.Domain, got[i].Domain)
					}
				}
			}
		})
	}
}

func TestParseMentionFromURI(t *testing.T) {
	tests := []struct {
		name   string
		uri    string
		expect Mention
	}{
		{
			name: "standard /users/ format",
			uri:  "https://example.com/users/test",
			expect: Mention{
				Username: "test",
				Domain:   "example.com",
				URI:      "https://example.com/users/test",
				Acct:     "test@example.com",
			},
		},
		{
			name: "@ username format",
			uri:  "https://example.com/@test",
			expect: Mention{
				Username: "test",
				Domain:   "example.com",
				URI:      "https://example.com/@test",
				Acct:     "test@example.com",
			},
		},
		{
			name: "/profiles/ format",
			uri:  "https://example.com/profiles/test",
			expect: Mention{
				Username: "test",
				Domain:   "example.com",
				URI:      "https://example.com/profiles/test",
				Acct:     "test@example.com",
			},
		},
		{
			name: "with trailing slash",
			uri:  "https://example.com/users/test/",
			expect: Mention{
				Username: "test",
				Domain:   "example.com",
				URI:      "https://example.com/users/test/",
				Acct:     "test@example.com",
			},
		},
		{
			name: "invalid URL",
			uri:  "not a url",
			expect: Mention{
				URI: "not a url",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseMentionFromURI(tt.uri)
			assert.Equal(t, tt.expect.Username, result.Username)
			assert.Equal(t, tt.expect.Domain, result.Domain)
			assert.Equal(t, tt.expect.URI, result.URI)
		})
	}
}

func TestParseMentionName(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		expectUser   string
		expectDomain string
	}{
		{
			name:         "remote user",
			input:        "@user@example.com",
			expectUser:   "user",
			expectDomain: "example.com",
		},
		{
			name:         "local user",
			input:        "@user",
			expectUser:   "user",
			expectDomain: "",
		},
		{
			name:         "no @ prefix",
			input:        "user@example.com",
			expectUser:   "user",
			expectDomain: "example.com",
		},
		{
			name:         "with whitespace",
			input:        "  @user@example.com  ",
			expectUser:   "user",
			expectDomain: "example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, domain := parseMentionName(tt.input)
			assert.Equal(t, tt.expectUser, user)
			assert.Equal(t, tt.expectDomain, domain)
		})
	}
}

func TestFormatAcct(t *testing.T) {
	tests := []struct {
		username string
		domain   string
		expected string
	}{
		{"user", "example.com", "user@example.com"},
		{"user", "", "user"},
		{"test_user", "social.network", "test_user@social.network"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatAcct(tt.username, tt.domain)
			assert.Equal(t, tt.expected, result)
		})
	}
}
