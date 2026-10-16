package moderation

import (
	"fmt"
	"unicode/utf8"

	"github.com/kasuganosora/community/systemsettings"
)

// Update holds the fields an admin changes; nil fields are left alone.
type Update struct {
	UGCVisibilityForVisitor      *string   `json:"ugc_visibility_for_visitor"`
	PreservedUsernames           *[]string `json:"preserved_usernames"`
	SensitiveWords               *[]string `json:"sensitive_words"`
	ProhibitedWords              *[]string `json:"prohibited_words"`
	ProhibitedWordsForNameOfUser *[]string `json:"prohibited_words_for_name_of_user"`
	HiddenTags                   *[]string `json:"hidden_tags"`
	BlockedHosts                 *[]string `json:"blocked_hosts"`
	SilencedHosts                *[]string `json:"silenced_hosts"`
	MediaSilencedHosts           *[]string `json:"media_silenced_hosts"`
	AutoCloseRegistration        *bool     `json:"auto_close_registration"`
}

const (
	maxEntries     = 5000
	maxEntryLength = 1024
)

// Changes validates u against the current snapshot and returns the
// normalised system_settings values to store.
func (u Update) Changes() (map[string]any, error) {
	cur := Current()
	out := map[string]any{}
	lines := func(key, label string, v *[]string, words bool) error {
		if v == nil {
			return nil
		}
		l := NormalizeLines(*v)
		if len(l) > maxEntries {
			return fmt.Errorf("%s: too many entries", label)
		}
		for _, e := range l {
			if utf8.RuneCountInString(e) > maxEntryLength {
				return fmt.Errorf("%s: entry too long", label)
			}
		}
		if words {
			if bad, ok := ValidateWords(l); !ok {
				return fmt.Errorf("%s: invalid regular expression %q", label, bad)
			}
		}
		out[key] = l
		return nil
	}
	if u.UGCVisibilityForVisitor != nil {
		v := *u.UGCVisibilityForVisitor
		if v != UGCAll && v != UGCLocal && v != UGCNone {
			return nil, fmt.Errorf("invalid ugc_visibility_for_visitor")
		}
		out[KeyUGCVisibilityForVisitor] = v
	}
	if u.AutoCloseRegistration != nil {
		out[KeyAutoCloseRegistration] = *u.AutoCloseRegistration
	}
	for _, f := range []struct {
		key, label string
		v          *[]string
		words      bool
	}{
		{KeyPreservedUsernames, "preserved_usernames", u.PreservedUsernames, false},
		{KeySensitiveWords, "sensitive_words", u.SensitiveWords, true},
		{KeyProhibitedWords, "prohibited_words", u.ProhibitedWords, true},
		{KeyProhibitedWordsForNameOfUser, "prohibited_words_for_name_of_user", u.ProhibitedWordsForNameOfUser, true},
		{KeyHiddenTags, "hidden_tags", u.HiddenTags, false},
	} {
		if err := lines(f.key, f.label, f.v, f.words); err != nil {
			return nil, err
		}
	}
	blocked := cur.BlockedHosts
	if u.BlockedHosts != nil {
		blocked = NormalizeHosts(*u.BlockedHosts, nil)
		if len(blocked) > maxEntries {
			return nil, fmt.Errorf("blocked_hosts: too many entries")
		}
		out[KeyBlockedHosts] = blocked
	}
	// Like Misskey, the silenced lists never repeat a blocked host.
	silenced, media := u.SilencedHosts, u.MediaSilencedHosts
	if u.BlockedHosts != nil {
		if silenced == nil {
			silenced = &cur.SilencedHosts
		}
		if media == nil {
			media = &cur.MediaSilencedHosts
		}
	}
	if silenced != nil {
		out[KeySilencedHosts] = NormalizeHosts(*silenced, blocked)
	}
	if media != nil {
		out[KeyMediaSilencedHosts] = NormalizeHosts(*media, blocked)
	}
	for k, v := range out {
		if l, ok := v.([]string); ok && len(l) > maxEntries {
			return nil, fmt.Errorf("%s: too many entries", k)
		}
	}
	return out, nil
}

// Save validates and persists u, then drops the compiled cache.
func Save(u Update) error {
	changes, err := u.Changes()
	if err != nil {
		return err
	}
	return SaveRaw(changes)
}

// SaveRaw persists already-normalised values and invalidates the cache.
func SaveRaw(changes map[string]any) error {
	if len(changes) == 0 {
		return nil
	}
	if err := systemsettings.SaveChanges(changes); err != nil {
		return err
	}
	Invalidate()
	return nil
}

// SetHostListed adds or removes host from one of the host lists
// (KeyBlockedHosts, KeySilencedHosts, KeyMediaSilencedHosts).
func SetHostListed(key, host string, listed bool) error {
	host = NormalizeHost(host)
	if host == "" {
		return fmt.Errorf("invalid host")
	}
	cur := Current()
	var list []string
	switch key {
	case KeyBlockedHosts:
		list = cur.BlockedHosts
	case KeySilencedHosts:
		list = cur.SilencedHosts
	case KeyMediaSilencedHosts:
		list = cur.MediaSilencedHosts
	default:
		return fmt.Errorf("unknown host list")
	}
	next := make([]string, 0, len(list)+1)
	for _, h := range list {
		if h != host {
			next = append(next, h)
		}
	}
	if listed {
		next = append(next, host)
	}
	u := Update{}
	switch key {
	case KeyBlockedHosts:
		u.BlockedHosts = &next
	case KeySilencedHosts:
		u.SilencedHosts = &next
	default:
		u.MediaSilencedHosts = &next
	}
	return Save(u)
}
