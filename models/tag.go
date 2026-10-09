package models

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kasuganosora/community/utils"
	"golang.org/x/net/html"
	"golang.org/x/text/unicode/norm"
	"gorm.io/gorm"
)

type Tag struct {
	Id               uint64     `gorm:"primary_key;autoIncrement"      json:"id"`
	Name             string     `gorm:"type:varchar(64);not null;index" json:"name"`
	StatusCount      uint64     `gorm:"not null;default:0"              json:"status_count"`
	AccountCount     uint64     `gorm:"not null;default:0"              json:"account_count"`
	LastStatusUsedAt *time.Time `gorm:"index"                           json:"last_status_used_at"`
	CreatedAt        *time.Time `gorm:"not null;autoCreateTime;index"   json:"created_at"`
	UpdatedAt        *time.Time `gorm:"not null;autoUpdateTime"         json:"updated_at"`
}

func (t *Tag) TableName() string {
	return "tags"
}

// ErrInvalidTagName is returned when a tag row would get a name that is not
// a valid hashtag (markup, entities, punctuation, whitespace...).
var ErrInvalidTagName = errors.New("invalid tag name")

// BeforeCreate refuses invalid names: tags are created from federated
// content, and a bad parser upstream must not be able to store garbage.
func (t *Tag) BeforeCreate(*gorm.DB) error {
	if !ValidTagName(t.Name) {
		return fmt.Errorf("%w: %q", ErrInvalidTagName, t.Name)
	}
	return nil
}

type Tags []*Tag

// MaxTagNameLength matches tags.name (varchar(64)).
const MaxTagNameLength = 64

// isTagRune reports whether r may appear in a hashtag: letters, combining
// marks, digits, underscore, middle dot and ZWNJ (the set Mastodon accepts).
func isTagRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) || r == '_' || r == '·' || r == '\u200c'
}

// tagPattern finds "#name" where # starts the text or follows a character
// that cannot be part of a word, a URL path or an HTML entity ("&#39;").
var tagPattern = regexp.MustCompile(`(?:^|[^\p{L}\p{M}\p{N}_/&#])#([\p{L}\p{M}\p{N}_·\x{200C}]+)`)

// NormalizeTagName turns "#Name" or " Name " into the stored form: leading
// '#' removed, surrounding whitespace trimmed, Unicode NFC. Case is
// preserved, exactly like tags parsed from local posts.
func NormalizeTagName(raw string) string {
	name := strings.TrimSpace(raw)
	name = strings.TrimLeft(name, "#")
	return norm.NFC.String(strings.TrimSpace(name))
}

// ValidTagName reports whether name (already normalized) is a hashtag:
// 1-64 tag characters, not made of digits (and hyphens) only. Hyphens are
// accepted because Misskey and Pleroma hashtags may contain them (they
// arrive as structured Hashtag tags); text parsing stops at them, like
// Mastodon. Anything containing markup, entities, quotes, ';', '&' or
// whitespace is rejected.
func ValidTagName(name string) bool {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > MaxTagNameLength {
		return false
	}
	numeric := true
	for _, r := range name {
		if !isTagRune(r) && r != '-' {
			return false
		}
		if !unicode.IsDigit(r) && r != '-' {
			numeric = false
		}
	}
	return !numeric
}

// TagText returns the visible text of a status body: HTML tags are dropped
// and entities decoded. Plain text passes through unchanged apart from
// entity decoding, so it is safe for local and remote content alike.
func TagText(content string) string {
	if !strings.ContainsAny(content, "<&") {
		return content
	}
	z := html.NewTokenizer(strings.NewReader(content))
	var out strings.Builder
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out.String()
		case html.TextToken:
			out.Write(z.Text())
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			// Block boundaries separate words; inline markup (Mastodon's
			// <a class="hashtag">#<span>tag</span></a>) must not.
			name, _ := z.TagName()
			switch string(name) {
			case "p", "br", "div", "li", "ul", "ol", "blockquote", "pre", "h1", "h2", "h3", "h4", "h5", "h6":
				out.WriteByte('\n')
			}
		}
	}
}

// ExtractTagNames returns the normalized, valid, de-duplicated hashtag
// names in text (plain text or HTML), in order of appearance.
func ExtractTagNames(text string) []string {
	matches := tagPattern.FindAllStringSubmatch(TagText(text), -1)
	names := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, m := range matches {
		name := NormalizeTagName(m[1])
		if !ValidTagName(name) || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// CleanTagNames normalizes names taken from structured data (ActivityPub
// Hashtag tags) and drops invalid ones and duplicates.
func CleanTagNames(raw []string) []string {
	names := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, r := range raw {
		name := NormalizeTagName(r)
		if !ValidTagName(name) || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

type structuredTagsKey struct{}

// WithStructuredTagNames makes names (e.g. the Hashtag entries of an
// ActivityPub object) authoritative for the status content written with ctx,
// instead of parsing the content.
func WithStructuredTagNames(ctx context.Context, names []string) context.Context {
	return context.WithValue(ctx, structuredTagsKey{}, CleanTagNames(names))
}

// StructuredTagNames returns the names set by WithStructuredTagNames.
func StructuredTagNames(ctx context.Context) ([]string, bool) {
	if ctx == nil {
		return nil, false
	}
	names, ok := ctx.Value(structuredTagsKey{}).([]string)
	return names, ok
}

// MatchTags returns the hashtags in str as "#name", de-duplicated.
func MatchTags(str string) []string {
	names := ExtractTagNames(str)
	tags := make([]string, 0, len(names))
	for _, name := range names {
		tags = append(tags, "#"+name)
	}
	return utils.FilterDuplicateAndEmptyString(tags)
}

// GetTagsFromText returns the existing tags used in text, keyed by name.
func GetTagsFromText(dbConn *gorm.DB, text string) (ret map[string]*Tag, err error) {
	names := ExtractTagNames(text)
	if len(names) == 0 {
		return
	}

	tags := make([]*Tag, 0, len(names))

	err = dbConn.Where("name IN ?", names).Find(&tags).Error
	if err != nil {
		return
	}

	ret = make(map[string]*Tag, len(tags))
	for _, tag := range tags {
		ret[tag.Name] = tag
	}

	return
}
