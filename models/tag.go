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
	"gorm.io/gorm/clause"
)

type Tag struct {
	Id   uint64 `gorm:"primary_key;autoIncrement"      json:"id"`
	Name string `gorm:"type:varchar(64);not null;index" json:"name"`
	// NameKey is TagKey(Name): the case-insensitive identity of the tag.
	// Name keeps the first-seen spelling for display, like Mastodon.
	NameKey          string     `gorm:"type:varchar(64);not null;default:'';uniqueIndex:uidx_tags_name_key" json:"-"`
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
	t.NameKey = TagKey(t.Name)
	return nil
}

// TagKey is the case-insensitive identity of a tag name: normalized (no
// '#', trimmed, NFC) and lowercased with Go's Unicode case mapping. It is
// computed in Go and compared exactly in SQL, so SQLite, MySQL and
// PostgreSQL agree regardless of their collations.
func TagKey(name string) string {
	return norm.NFC.String(strings.ToLower(NormalizeTagName(name)))
}

// FindTagByName returns the tag whose name matches name case-insensitively.
func FindTagByName(conn *gorm.DB, name string) (*Tag, error) {
	key := TagKey(name)
	if key == "" {
		return nil, gorm.ErrRecordNotFound
	}
	var tag Tag
	if err := conn.Where("name_key = ?", key).First(&tag).Error; err != nil {
		return nil, err
	}
	return &tag, nil
}

// FindTagsByNames returns the existing tags matching names, keyed by TagKey.
func FindTagsByNames(conn *gorm.DB, names []string) (map[string]*Tag, error) {
	keys := make([]string, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		if key := TagKey(name); key != "" && !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	ret := make(map[string]*Tag, len(keys))
	if len(keys) == 0 {
		return ret, nil
	}
	var tags []*Tag
	if err := conn.Where("name_key IN ?", keys).Find(&tags).Error; err != nil {
		return nil, err
	}
	for _, tag := range tags {
		ret[tag.NameKey] = tag
	}
	return ret, nil
}

// FindOrCreateTags returns one tag per distinct valid name, keyed by
// TagKey, creating the missing ones with the given spelling. Creation is
// race-safe: a concurrent insert of the same key is absorbed by the unique
// index and the existing row is returned.
func FindOrCreateTags(conn *gorm.DB, names []string) (map[string]*Tag, error) {
	names = CleanTagNames(names)
	tags, err := FindTagsByNames(conn, names)
	if err != nil {
		return nil, err
	}
	missing := make([]*Tag, 0)
	for _, name := range names {
		key := TagKey(name)
		if _, ok := tags[key]; ok {
			continue
		}
		// Set the key here too: callers may run with SkipHooks (bulk import).
		tag := &Tag{Name: name, NameKey: key}
		tags[key] = tag
		missing = append(missing, tag)
	}
	if len(missing) == 0 {
		return tags, nil
	}
	if err := conn.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name_key"}}, DoNothing: true}).Create(&missing).Error; err != nil {
		return nil, err
	}
	// Re-read: ids of rows skipped on conflict are not filled in.
	return FindTagsByNames(conn, names)
}

// FindOrCreateTag is FindOrCreateTags for one name.
func FindOrCreateTag(conn *gorm.DB, name string) (*Tag, error) {
	tags, err := FindOrCreateTags(conn, []string{name})
	if err != nil {
		return nil, err
	}
	tag, ok := tags[TagKey(name)]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrInvalidTagName, name)
	}
	return tag, nil
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
	// The lowercased key must fit tags.name_key too (a few runes, e.g.
	// 'İ', grow when lowercased).
	if utf8.RuneCountInString(strings.ToLower(name)) > MaxTagNameLength {
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

// GetTagsFromText returns the existing tags used in text, keyed by name key.
func GetTagsFromText(dbConn *gorm.DB, text string) (ret map[string]*Tag, err error) {
	names := ExtractTagNames(text)
	if len(names) == 0 {
		return
	}
	return FindTagsByNames(dbConn, names)
}
