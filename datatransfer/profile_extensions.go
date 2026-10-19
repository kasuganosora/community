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
	if b := strings.TrimSpace(a.Birthday); b != "" {
		if d, err := time.Parse("2006-01-02", b); err == nil && len(b) == 10 && d.Year() >= 1 && !d.After(time.Now().Add(24*time.Hour)) {
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
	for _, uri := range a.AlsoKnownAs {
		if safeHTTPURL(uri) && len(uri) <= maxActorURILength {
			target.AlsoKnownAs = append(target.AlsoKnownAs, uri)
		} else {
			issue("profile_value_dropped", "also_known_as", "an alias URI is not a valid http(s) URL")
		}
	}
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
