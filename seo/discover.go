package seo

// Site-level discovery terms. People still search the names of the networks
// this kind of server belongs to (Misskey, Mastodon, the fediverse,
// ActivityPub). They belong on the homepage and about page only — repeating
// the same keyword block on every profile and post would look like stuffing.

const siteKeywords = "misskey, mastodon, fediverse, activitypub, misskey server, mastodon server, fediverse instance, activitypub server, misskey instance, mastodon instance"

const homeDiscovery = "Fediverse server on ActivityPub: people find this instance by searching Misskey, Mastodon, and the fediverse."

const aboutDiscovery = "About this fediverse community. It speaks ActivityPub, so Misskey and Mastodon users can find and follow it."

func withDiscovery(blurb, sentence string) string {
	blurb = excerpt(blurb, 180)
	if blurb == "" {
		return sentence
	}
	return blurb + " " + sentence
}
