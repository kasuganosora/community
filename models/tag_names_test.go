package models

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractTagNamesFromHTML(t *testing.T) {
	cases := map[string][]string{
		// Mastodon hashtag markup.
		`<p>Sirens <a href="https://m.example/tags/AirAlert" class="mention hashtag" rel="tag">#<span>AirAlert</span></a></p>`: {"AirAlert"},
		// Entities must not become tags (old parser stored "39;ll" and "34;prompters,&#34;").
		`<p>I&#39;ll ask the &#34;prompters,&#34; about #ok</p>`: {"ok"},
		`&#35;decoded and #real`:                                 {"decoded", "real"},
		`<p>#one</p><p>#two</p>`:                                 {"one", "two"},
		`#golang, #rust. (#zig)`:                                 {"golang", "rust", "zig"},
		`#你好 #Café #mixed_case #a·b`:                             {"你好", "Café", "mixed_case", "a·b"},
		`https://example.com/page#anchor and a#b`:                nil,
		`#123 #2024year`:                                         {"2024year"},
		`#dup #dup`:                                              {"dup"},
		`<a href="#notatag">x</a> <code>#tag</code>`:             {"tag"},
	}
	for in, want := range cases {
		got := ExtractTagNames(in)
		if want == nil {
			want = []string{}
		}
		assert.Equal(t, want, got, in)
	}
}

func TestValidTagNameRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "<span>AirAlert</span></a></p>", "39;ll", "34;prompters,&#34;", "a<b", "a>b", "a&b", "a;b",
		`a"b`, "a'b", "a b", "a\tb", "#tag", "123", "1-2", strings.Repeat("a", 65)} {
		assert.False(t, ValidTagName(bad), bad)
	}
	for _, good := range []string{"AirAlert", "你好", "café", "a_b", "x1", "foo-bar", strings.Repeat("a", 64)} {
		assert.True(t, ValidTagName(good), good)
	}
	assert.Equal(t, "café", NormalizeTagName("#cafe\u0301 "), "NFC")
}

func TestTagBeforeCreateRejectsInvalidNames(t *testing.T) {
	require.ErrorIs(t, (&Tag{Name: "39;ll"}).BeforeCreate(nil), ErrInvalidTagName)
	require.NoError(t, (&Tag{Name: "AirAlert"}).BeforeCreate(nil))
}

func TestStructuredTagNamesAreCleaned(t *testing.T) {
	names, ok := StructuredTagNames(WithStructuredTagNames(context.Background(), []string{"#AirAlert", "#AirAlert", "<b>x</b>", " #ok "}))
	require.True(t, ok)
	assert.Equal(t, []string{"AirAlert", "ok"}, names)
	_, ok = StructuredTagNames(context.Background())
	assert.False(t, ok)
}

func TestMatchTagsKeepsHashPrefix(t *testing.T) {
	assert.Equal(t, []string{"#golang"}, MatchTags("<p>#<span>golang</span>, yes</p>"))
}
