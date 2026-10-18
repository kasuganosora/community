package streaming

import (
	"strings"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
)

// visitorStreamAllowed applies Misskey's ugcVisibilityForVisitor to the
// generic /streaming websocket for logged-out connections: "none" refuses
// every timeline stream, "local" only allows the local-only public and
// hashtag streams. Signed-in accounts are never affected.
func visitorStreamAllowed(account *models.Account, stream string) bool {
	if account != nil {
		return true
	}
	if !strings.HasPrefix(stream, "public") && !strings.HasPrefix(stream, "hashtag") {
		return true // other streams already require login
	}
	switch moderation.Current().UGCVisibilityForVisitor {
	case moderation.UGCNone:
		return false
	case moderation.UGCLocal:
		for _, part := range strings.Split(stream, ":")[1:] {
			if part == "local" {
				return true
			}
		}
		return false
	}
	return true
}

const visitorRestrictedError = "This content is restricted by the server for visitors. Please sign in."

// visitorCanSeeStatuses drops events a logged-out visitor may not see under
// ugcVisibilityForVisitor (author or reblogged author on a disallowed host)
// or by an account that requires sign-in to view.
func visitorCanSeeStatuses(statuses models.Statuses) bool {
	snap := moderation.Current()
	for _, st := range statuses {
		for cur := st; cur != nil; cur = cur.Reblog {
			if cur.Account == nil {
				continue
			}
			host := ""
			if cur.Account.Domain != nil {
				host = *cur.Account.Domain
			}
			if !snap.GuestCanSee(host) || cur.Account.HiddenFromVisitors() {
				return false
			}
			if cur.Reblog == cur {
				break
			}
		}
	}
	return true
}
