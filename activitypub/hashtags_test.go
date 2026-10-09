package activitypub

import (
	"sort"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func statusTagNames(t *testing.T, conn *gorm.DB, originURI string) []string {
	t.Helper()
	var names []string
	require.NoError(t, conn.Table("tags").Joins("JOIN status_tags ON status_tags.tag_id = tags.id").
		Joins("JOIN statuses ON statuses.id = status_tags.status_id").
		Where("statuses.origin_uri = ?", originURI).Pluck("tags.name", &names).Error)
	sort.Strings(names)
	return names
}

func aliceCreate(n string, object map[string]any) map[string]any {
	return map[string]any{"id": e2eOrigin + "/users/alice/statuses/" + n + "/activity", "type": "Create",
		"actor": e2eOrigin + "/users/alice", "to": []string{asPublicURI}, "object": object}
}

const mastodonTagHTML = `<p>Sirens again <a href="https://origin.test/tags/AirAlert" class="mention hashtag" rel="tag">#<span>AirAlert</span></a> I&#39;ll stay &#34;prompters,&#34; <a href="https://origin.test/tags/kyiv" class="mention hashtag" rel="tag">#<span>Kyiv</span></a></p>`

func TestRemoteHashtagsComeFromTheTagArray(t *testing.T) {
	w := newRelayWorld(t)
	alice := NewClient(e2eOrigKey, e2eOrigin+"/users/alice")
	note := originNote("40", mastodonTagHTML)
	note["tag"] = []any{
		map[string]any{"type": "Hashtag", "href": e2eOrigin + "/tags/airalert", "name": "#AirAlert"},
		map[string]any{"type": "Hashtag", "href": e2eOrigin + "/tags/kyiv", "name": "#Kyiv"},
		map[string]any{"type": "Hashtag", "name": "#<b>bad</b>"},
		map[string]any{"type": "Hashtag", "name": "#misskey-style"}, // Misskey allows hyphens
		map[string]any{"type": "Mention", "href": e2eBase + "/users/nobody", "name": "@nobody"},
	}
	require.Equal(t, 200, w.postStatus(t, alice, e2eBase+"/inbox", aliceCreate("40", note)))
	require.Equal(t, []string{"AirAlert", "Kyiv", "misskey-style"}, statusTagNames(t, w.conn, e2eOrigin+"/users/alice/statuses/40"))

	// Update{Note} re-syncs the tags from the new tag array.
	note["content"] = `<p>now <a class="hashtag">#<span>Odesa</span></a></p>`
	note["tag"] = []any{map[string]any{"type": "Hashtag", "name": "#Odesa"}}
	update := map[string]any{"id": e2eOrigin + "/users/alice/statuses/40#updates/1", "type": "Update",
		"actor": e2eOrigin + "/users/alice", "to": []string{asPublicURI}, "object": note}
	require.Equal(t, 200, w.postStatus(t, alice, e2eBase+"/inbox", update))
	require.Equal(t, []string{"Odesa"}, statusTagNames(t, w.conn, e2eOrigin+"/users/alice/statuses/40"))

	// An empty tag array is authoritative: no tags, even if the text has '#'.
	note["content"] = `<p>#notatag</p>`
	note["tag"] = []any{}
	update["id"] = e2eOrigin + "/users/alice/statuses/40#updates/2"
	require.Equal(t, 200, w.postStatus(t, alice, e2eBase+"/inbox", update))
	require.Empty(t, statusTagNames(t, w.conn, e2eOrigin+"/users/alice/statuses/40"))
}

func TestRemoteHashtagsFallBackToVisibleText(t *testing.T) {
	w := newRelayWorld(t)
	alice := NewClient(e2eOrigKey, e2eOrigin+"/users/alice")
	note := originNote("41", mastodonTagHTML) // no "tag" property
	require.Equal(t, 200, w.postStatus(t, alice, e2eBase+"/inbox", aliceCreate("41", note)))
	require.Equal(t, []string{"AirAlert", "Kyiv"}, statusTagNames(t, w.conn, e2eOrigin+"/users/alice/statuses/41"))

	var tags []models.Tag
	require.NoError(t, w.conn.Find(&tags).Error)
	for _, tag := range tags {
		require.True(t, models.ValidTagName(tag.Name), tag.Name)
	}
}

func TestRelayedHashtagsUseTheFetchedTagArray(t *testing.T) {
	w := newRelayWorld(t)
	w.subscribe(t)
	note := originNote("42", mastodonTagHTML)
	note["tag"] = []any{map[string]any{"type": "Hashtag", "name": "#AirAlert"}}
	w.origin.notes["/users/alice/statuses/42"] = note
	require.Equal(t, 200, w.postStatus(t, w.relay.client(), e2eBase+"/inbox", aliceCreate("42", originNote("42", "embedded"))))
	require.Equal(t, []string{"AirAlert"}, statusTagNames(t, w.conn, e2eOrigin+"/users/alice/statuses/42"))
}

// Relays forward activities they cannot be verified for (here wxw.moe's
// Undo{Announce} signed by mastodon-relay.moew.science); they are
// acknowledged and dropped instead of answered with 401.
func TestRelayForwardedUndoIsIgnoredNot401(t *testing.T) {
	w := newRelayWorld(t)
	w.subscribe(t)
	undo := map[string]any{"id": e2eOrigin + "/users/alice/statuses/9/activity/undo", "type": "Undo", "actor": e2eOrigin + "/users/alice",
		"to": []string{asPublicURI}, "object": map[string]any{"id": e2eOrigin + "/users/alice/statuses/9/activity", "type": "Announce",
			"actor": e2eOrigin + "/users/alice", "object": e2eOrigin + "/users/alice/statuses/9"}}
	require.Equal(t, 202, w.postStatus(t, w.relay.client(), e2eBase+"/inbox", undo))
	require.Equal(t, 202, w.postStatus(t, w.relay.client(), e2eBase+"/actor/inbox", undo))

	var accounts int64
	require.NoError(t, w.conn.Model(&models.Account{}).Where("origin_id = ?", e2eOrigin+"/users/alice").Count(&accounts).Error)
	require.Zero(t, accounts, "nothing was processed")
}
