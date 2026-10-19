package datatransfer

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kasuganosora/community/models"
	"golang.org/x/text/language"
)

// Limits match the profile update API (controllers/api/account).
const (
	maxLocationRunes        = 128
	maxFollowedMessageRunes = 200
	maxActorURILength       = 2048
)

// applyProfileExtensions copies Misskey profile extensions onto a new target
// account, applying the same rules the profile API enforces. A value the
// target cannot hold is reported (profile_value_dropped / _truncated) rather
// than failing the account.
func (i *importer) applyProfileExtensions(target *models.Account, a Account) {
	issue := func(code, field, msg string) {
		i.report.Issues = append(i.report.Issues, Issue{Severity: "info", Code: code, Kind: "account." + field, SourceID: a.ID, Message: msg})
	}
	target.IsCat = a.IsCat
	if loc := strings.TrimSpace(a.Location); loc != "" {
		if utf8.RuneCountInString(loc) > maxLocationRunes {
			loc = truncateRunes(loc, maxLocationRunes)
			issue("profile_value_truncated", "location", "location longer than 128 characters was truncated")
		}
		target.Location = loc
	}
	if strings.TrimSpace(a.Birthday) != "" {
		if b, ok := validBirthday(a.Birthday, time.Now()); ok {
			target.Birthday = b
		} else {
			issue("profile_value_dropped", "birthday", "birthday is not a valid past YYYY-MM-DD date")
		}
	}
	if l := strings.TrimSpace(a.Language); l != "" {
		if tag, err := language.Parse(l); err == nil && len(l) <= 35 {
			target.Language = tag.String()
		} else {
			issue("profile_value_dropped", "language", "language is not a valid language tag")
		}
	}
	if m := a.FollowedMessage; m != "" {
		if utf8.RuneCountInString(m) > maxFollowedMessageRunes {
			m = truncateRunes(m, maxFollowedMessageRunes)
			issue("profile_value_truncated", "followed_message", "followed message longer than 200 characters was truncated")
		}
		target.FollowedMessage = m
	}
	if i.personal() {
		// Migration state comes only from the operator's source database,
		// never from an uploaded personal archive.
		return
	}
	if a.MovedTo != "" {
		if safeHTTPURL(a.MovedTo) && len(a.MovedTo) <= maxActorURILength {
			target.MovedTo = a.MovedTo
		} else {
			issue("profile_value_dropped", "moved_to", "moved-to URI is not a valid http(s) URL")
		}
	}
	for _, uri := range splitAlsoKnownAs(a.AlsoKnownAs) {
		if validActorURI(uri) {
			target.AlsoKnownAs = append(target.AlsoKnownAs, uri)
		} else {
			issue("profile_value_dropped", "also_known_as", "an alias URI is not a valid http(s) URL")
		}
	}
}

// Value rules shared with scripts/migration/export_misskey.py
// (split_also_known_as, valid_actor_uri, valid_birthday); both run the vectors
// in scripts/migration/testdata/misskey_value_rules.json. See "Value rules" in
// docs/misskey-field-mapping.md.

// splitAlsoKnownAs normalizes Misskey alsoKnownAs values: upstream stores the
// column as a TypeORM simple-array (one comma-joined text value), so every
// element is split on ',', trimmed, and empty or repeated URIs are removed
// (source order kept).
func splitAlsoKnownAs(values []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" && !seen[part] {
				seen[part] = true
				out = append(out, part)
			}
		}
	}
	return out
}

// validActorURI is an absolute http(s) URL of at most 2048 bytes.
func validActorURI(uri string) bool {
	return safeHTTPURL(uri) && len(uri) <= maxActorURILength
}

// validBirthday returns the trimmed birthday when it is a real YYYY-MM-DD
// date with year >= 1 (Misskey allows 0000) not later than one day after now.
func validBirthday(raw string, now time.Time) (string, bool) {
	b := strings.TrimSpace(raw)
	d, err := time.Parse("2006-01-02", b)
	if err != nil || len(b) != 10 || d.Year() < 1 || d.After(now.Add(24*time.Hour)) {
		return "", false
	}
	return b, true
}

func truncateRunes(s string, n int) string {
	for i := range s {
		if n == 0 {
			return s[:i]
		}
		n--
	}
	return s
}
