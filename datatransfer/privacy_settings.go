package datatransfer

import "github.com/kasuganosora/community/models"

// applyPrivacySettings copies Misskey followingVisibility /
// followersVisibility, autoAcceptFollowed, hideOnlineStatus, speakAsCat and
// lastActiveDate onto a new target account. Bundles without the per-list
// values keep the legacy hide_collections meaning (both lists private).
func applyPrivacySettings(target *models.Account, a Account) {
	following, followers := models.CollectionPublic, models.CollectionPublic
	if a.HideCollections {
		following, followers = models.CollectionPrivate, models.CollectionPrivate
	}
	if a.FollowingVisibility != "" || a.FollowersVisibility != "" {
		following, followers = models.CollectionPublic, models.CollectionPublic
		if models.ValidCollectionVisibility(a.FollowingVisibility) {
			following = a.FollowingVisibility
		}
		if models.ValidCollectionVisibility(a.FollowersVisibility) {
			followers = a.FollowersVisibility
		}
	}
	keepRemote := target.HideCollections
	target.SetCollectionVisibility(following, followers)
	if target.Domain != nil {
		// Remote rows keep the hide_collections the importer derived from
		// the remote actor.
		target.HideCollections = keepRemote || target.HideCollections
	}
	target.AutoAcceptFollowed = a.AutoAcceptFollowed
	target.HideOnlineStatus = a.HideOnlineStatus
	target.SpeakAsCat = a.SpeakAsCat == nil || *a.SpeakAsCat
	if a.LastActiveAt != nil && !a.LastActiveAt.IsZero() {
		at := *a.LastActiveAt
		target.LastActiveAt = &at
	}
}
