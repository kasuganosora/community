package models

import (
	"time"

	"gorm.io/gorm"
)

// Collection visibility values (Misskey followingVisibility / followersVisibility).
const (
	CollectionPublic    = "public"
	CollectionFollowers = "followers"
	CollectionPrivate   = "private"
)

// ValidCollectionVisibility reports whether v is a known visibility value.
func ValidCollectionVisibility(v string) bool {
	return v == CollectionPublic || v == CollectionFollowers || v == CollectionPrivate
}

func validOr(v, fallback string) string {
	if ValidCollectionVisibility(v) {
		return v
	}
	return fallback
}

// collectionVisibilities returns the effective (following, followers)
// visibility. A row whose legacy hide_collections is set while both lists
// still say public (written by code or imports that only know the Mastodon
// flag) hides both lists.
func (a *Account) collectionVisibilities() (string, string) {
	following, followers := validOr(a.FollowingVisibility, CollectionPublic), validOr(a.FollowersVisibility, CollectionPublic)
	if a.HideCollections && following == CollectionPublic && followers == CollectionPublic {
		return CollectionPrivate, CollectionPrivate
	}
	return following, followers
}

// FollowingVisibilityValue returns the effective following-list visibility.
func (a *Account) FollowingVisibilityValue() string {
	v, _ := a.collectionVisibilities()
	return v
}

// FollowersVisibilityValue returns the effective followers-list visibility.
func (a *Account) FollowersVisibilityValue() string {
	_, v := a.collectionVisibilities()
	return v
}

// SetCollectionVisibility updates both visibilities and keeps the legacy
// HideCollections flag as "either list is not public".
func (a *Account) SetCollectionVisibility(following, followers string) {
	a.FollowingVisibility, a.FollowersVisibility = following, followers
	a.HideCollections = following != CollectionPublic || followers != CollectionPublic
}

// CollectionVisibilityUpdates returns the column updates for the given
// visibilities, including the derived hide_collections flag.
func CollectionVisibilityUpdates(following, followers string) map[string]any {
	return map[string]any{
		"following_visibility": following,
		"followers_visibility": followers,
		"hide_collections":     following != CollectionPublic || followers != CollectionPublic,
	}
}

// CanViewCollection reports whether viewer may list the account's following
// (followers=false) or followers (followers=true). The owner always can;
// "followers" visibility admits accepted followers of the account.
func (a *Account) CanViewCollection(conn *gorm.DB, viewer *Account, followers bool) bool {
	visibility := a.FollowingVisibilityValue()
	if followers {
		visibility = a.FollowersVisibilityValue()
	}
	if viewer != nil && viewer.Id == a.Id {
		return true
	}
	switch visibility {
	case CollectionPublic:
		return true
	case CollectionFollowers:
		if viewer == nil || conn == nil {
			return false
		}
		var n int64
		conn.Model(&Follow{}).Scopes(Followed).Where("account_id = ? AND follow_id = ?", viewer.Id, a.Id).Count(&n)
		return n > 0
	default:
		return false
	}
}

// Misskey online-status thresholds.
const (
	OnlineThreshold = 10 * time.Minute
	ActiveThreshold = 3 * 24 * time.Hour
)

// OnlineStatus returns Misskey's onlineStatus: online, active, offline, or
// unknown when hidden or never recorded.
func (a *Account) OnlineStatus(now time.Time) string {
	if a.HideOnlineStatus || a.LastActiveAt == nil {
		return "unknown"
	}
	elapsed := now.Sub(*a.LastActiveAt)
	switch {
	case elapsed < OnlineThreshold:
		return "online"
	case elapsed < ActiveThreshold:
		return "active"
	default:
		return "offline"
	}
}

// CanFollowersPublic reports whether the followers list is public.
func (a *Account) CanFollowersPublic() bool { return a.FollowersVisibilityValue() == CollectionPublic }

// CanFollowingPublic reports whether the following list is public.
func (a *Account) CanFollowingPublic() bool { return a.FollowingVisibilityValue() == CollectionPublic }

// AutoAcceptsFollowFrom reports whether a follow request from follower to
// this local account is approved automatically because the account enabled
// autoAcceptFollowed and already follows follower (Misskey semantics).
func (a *Account) AutoAcceptsFollowFrom(conn *gorm.DB, follower *Account) bool {
	if conn == nil || follower == nil || a.Domain != nil || !a.AutoAcceptFollowed {
		return false
	}
	var n int64
	conn.Model(&Follow{}).Scopes(Followed).Where("account_id = ? AND follow_id = ?", a.Id, follower.Id).Count(&n)
	return n > 0
}
