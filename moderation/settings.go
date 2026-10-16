// Package moderation holds the instance moderation settings modelled on
// Misskey's admin "moderation" page: visitor content visibility, preserved
// usernames, sensitive / prohibited word lists, hidden tags and the
// blocked / silenced / media-silenced host lists.
//
// Values live in system_settings (keys below). Compiled matchers are cached
// and rebuilt after Save or when the cache is older than cacheTTL, so the
// hot paths (status creation, inbox, delivery) never compile patterns.
package moderation

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

const (
	KeyUGCVisibilityForVisitor      = "moderation.ugc_visibility_for_visitor"
	KeyPreservedUsernames           = "moderation.preserved_usernames"
	KeySensitiveWords               = "moderation.sensitive_words"
	KeyProhibitedWords              = "moderation.prohibited_words"
	KeyProhibitedWordsForNameOfUser = "moderation.prohibited_words_for_name_of_user"
	KeyHiddenTags                   = "moderation.hidden_tags"
	KeyBlockedHosts                 = "moderation.blocked_hosts"
	KeySilencedHosts                = "moderation.silenced_hosts"
	KeyMediaSilencedHosts           = "moderation.media_silenced_hosts"
	// KeyAutoCloseRegistration enables Misskey's automatic switch to
	// closed registration when no moderator was active for
	// ModeratorInactivityLimit.
	KeyAutoCloseRegistration = "moderation.auto_close_registration"
	// KeyRegistrationAutoClosedAt records when that last happened.
	KeyRegistrationAutoClosedAt = "moderation.registration_auto_closed_at"
)

// Visitor content visibility values (Misskey ugcVisibilityForVisitor).
const (
	UGCAll   = "all"
	UGCLocal = "local"
	UGCNone  = "none"
)

// DefaultPreservedUsernames is Misskey's default meta.preservedUsernames.
var DefaultPreservedUsernames = []string{
	"admin", "administrator", "root", "system", "maintainer", "host", "mod", "moderator", "owner", "superuser", "staff", "auth", "i", "me", "everyone", "all", "mention", "mentions", "example", "user", "users", "account", "accounts", "official", "help", "helps", "support", "supports", "info", "information", "informations", "announce", "announces", "announcement", "announcements", "notice", "notification", "notifications", "dev", "developer", "developers", "tech", "misskey",
}

const cacheTTL = 30 * time.Second

// Snapshot is an immutable, compiled view of the moderation settings.
type Snapshot struct {
	UGCVisibilityForVisitor      string
	PreservedUsernames           []string
	SensitiveWords               []string
	ProhibitedWords              []string
	ProhibitedWordsForNameOfUser []string
	HiddenTags                   []string
	BlockedHosts                 []string
	SilencedHosts                []string
	MediaSilencedHosts           []string
	AutoCloseRegistration        bool

	sensitive     *WordMatcher
	prohibited    *WordMatcher
	prohibitedNm  *WordMatcher
	preserved     map[string]bool
	hiddenTags    map[string]bool
	blocked       models.HostSet
	silenced      models.HostSet
	mediaSilenced map[string]bool
	builtAt       time.Time
}

var (
	current   atomic.Pointer[Snapshot]
	rebuildMu sync.Mutex
)

func init() {
	models.ModerationHostLists = func() (blocked, silenced []string) {
		s := Current()
		return s.BlockedHosts, s.SilencedHosts
	}
}

// Invalidate drops the cached snapshot; the next Current rebuilds it.
func Invalidate() { current.Store(nil) }

// Current returns the cached snapshot, rebuilding it when stale.
func Current() *Snapshot {
	if s := current.Load(); s != nil && time.Since(s.builtAt) < cacheTTL {
		return s
	}
	rebuildMu.Lock()
	defer rebuildMu.Unlock()
	if s := current.Load(); s != nil && time.Since(s.builtAt) < cacheTTL {
		return s
	}
	s := build()
	current.Store(s)
	return s
}

func stringsSetting(key string, def []string) []string {
	if systemsettings.Get(key, nil) == nil {
		return def
	}
	return systemsettings.GetStrings(key, nil)
}

func build() *Snapshot {
	s := &Snapshot{
		UGCVisibilityForVisitor:      NormalizeUGC(systemsettings.GetString(KeyUGCVisibilityForVisitor, UGCAll)),
		PreservedUsernames:           NormalizeLines(stringsSetting(KeyPreservedUsernames, DefaultPreservedUsernames)),
		SensitiveWords:               NormalizeLines(stringsSetting(KeySensitiveWords, nil)),
		ProhibitedWords:              NormalizeLines(stringsSetting(KeyProhibitedWords, nil)),
		ProhibitedWordsForNameOfUser: NormalizeLines(stringsSetting(KeyProhibitedWordsForNameOfUser, nil)),
		HiddenTags:                   NormalizeLines(stringsSetting(KeyHiddenTags, nil)),
		BlockedHosts:                 NormalizeHosts(stringsSetting(KeyBlockedHosts, nil), nil),
		AutoCloseRegistration:        systemsettings.GetBool(KeyAutoCloseRegistration, true),
		builtAt:                      time.Now(),
	}
	s.SilencedHosts = NormalizeHosts(stringsSetting(KeySilencedHosts, nil), s.BlockedHosts)
	s.MediaSilencedHosts = NormalizeHosts(stringsSetting(KeyMediaSilencedHosts, nil), s.BlockedHosts)
	s.compile()
	return s
}

func (s *Snapshot) compile() {
	s.sensitive = CompileWords(s.SensitiveWords)
	s.prohibited = CompileWords(s.ProhibitedWords)
	s.prohibitedNm = CompileWords(s.ProhibitedWordsForNameOfUser)
	s.preserved = map[string]bool{}
	for _, u := range s.PreservedUsernames {
		s.preserved[strings.ToLower(u)] = true
	}
	s.hiddenTags = map[string]bool{}
	for _, t := range s.HiddenTags {
		s.hiddenTags[TagKey(t)] = true
	}
	s.blocked = models.HostSet{}
	for _, h := range s.BlockedHosts {
		s.blocked[h] = true
	}
	s.silenced = models.HostSet{}
	for _, h := range s.SilencedHosts {
		s.silenced[h] = true
	}
	s.mediaSilenced = map[string]bool{}
	for _, h := range s.MediaSilencedHosts {
		s.mediaSilenced[h] = true
	}
}

// NormalizeUGC maps unknown values to "all".
func NormalizeUGC(v string) string {
	switch v {
	case UGCLocal, UGCNone:
		return v
	}
	return UGCAll
}

// NormalizeLines drops empty entries (Misskey: filter(Boolean)) and strips
// a trailing carriage return left by Windows line endings.
func NormalizeLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		out = append(out, l)
	}
	return out
}

// SplitLines splits textarea input into lines.
func SplitLines(text string) []string {
	return NormalizeLines(strings.Split(text, "\n"))
}

// NormalizeHost lower-cases a host and strips scheme, path, wildcard
// prefix and trailing dot, so "https://Bad.Example/" and "*.bad.example"
// both become "bad.example".
func NormalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.Index(h, "://"); i >= 0 {
		h = h[i+3:]
	}
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	h = strings.TrimPrefix(h, "*.")
	h = strings.TrimPrefix(h, ".")
	return strings.TrimSuffix(h, ".")
}

// NormalizeHosts normalises, sorts and de-duplicates hosts and removes
// those in exclude (Misskey keeps silenced/media-silenced lists disjoint
// from blockedHosts).
func NormalizeHosts(hosts []string, exclude []string) []string {
	skip := map[string]bool{}
	for _, h := range exclude {
		skip[h] = true
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = NormalizeHost(h)
		if h == "" || seen[h] || skip[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// TagKey normalises a hashtag for hidden-tag comparison.
func TagKey(t string) string {
	return models.TagKey(strings.TrimPrefix(strings.TrimSpace(t), "#"))
}

// --- queries -----------------------------------------------------------

// IsSensitiveText reports whether text contains a sensitive word.
func (s *Snapshot) IsSensitiveText(text string) bool { return s.sensitive.Match(text) }

// HasProhibitedWords reports whether text contains a prohibited word.
func (s *Snapshot) HasProhibitedWords(text string) bool { return s.prohibited.Match(text) }

// IsProhibitedName reports whether a username or display name contains a
// word from prohibitedWordsForNameOfUser.
func (s *Snapshot) IsProhibitedName(name string) bool { return s.prohibitedNm.Match(name) }

// IsPreservedUsername reports whether username is reserved
// (case-insensitive, like Misskey's signup check).
func (s *Snapshot) IsPreservedUsername(username string) bool {
	return s.preserved[strings.ToLower(username)]
}

// IsHiddenTag reports whether a hashtag is hidden from trends and
// suggestions.
func (s *Snapshot) IsHiddenTag(tag string) bool {
	return len(s.hiddenTags) > 0 && s.hiddenTags[TagKey(tag)]
}

// IsBlockedHost is Misskey's isBlockedHost: host or a parent domain.
func (s *Snapshot) IsBlockedHost(host string) bool { return host != "" && s.blocked.Contains(host) }

// IsSilencedHost is Misskey's isSilencedHost: host or a parent domain.
func (s *Snapshot) IsSilencedHost(host string) bool { return host != "" && s.silenced.Contains(host) }

// IsMediaSilencedHost is Misskey's isMediaSilencedHost: exact host only.
func (s *Snapshot) IsMediaSilencedHost(host string) bool {
	return host != "" && s.mediaSilenced[strings.ToLower(host)]
}

// GuestCanSee reports whether a logged-out visitor may see content by an
// author on host ("" for local).
func (s *Snapshot) GuestCanSee(host string) bool {
	switch s.UGCVisibilityForVisitor {
	case UGCNone:
		return false
	case UGCLocal:
		return host == ""
	}
	return true
}
