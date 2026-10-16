package status

import (
	"net/url"
	"testing"

	"github.com/kasuganosora/community/moderation"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func withModerationWords(t *testing.T, sensitive, prohibited []string) {
	t.Helper()
	systemsettings.Set(moderation.KeySensitiveWords, sensitive)
	systemsettings.Set(moderation.KeyProhibitedWords, prohibited)
	moderation.Invalidate()
	t.Cleanup(func() {
		systemsettings.Set(moderation.KeySensitiveWords, []string{})
		systemsettings.Set(moderation.KeyProhibitedWords, []string{})
		moderation.Invalidate()
	})
}

func postForm(t *testing.T, ts *testSetup, form url.Values) (int, map[string]any) {
	t.Helper()
	return postRaw(t, ts, "application/x-www-form-urlencoded", []byte(form.Encode()))
}

func TestCreateSensitiveWordDowngradesPublicToUnlisted(t *testing.T) {
	ts := newTestSetup(t)
	withModerationWords(t, []string{"spoiler alert", "/秘密\\d+/"}, nil)

	code, resp := postForm(t, ts, url.Values{"status": {"big spoiler here, alert"}, "visibility": {"public"}})
	require.Equal(t, 200, code, resp)
	require.Equal(t, "unlisted", resp["visibility"], "AND words downgrade public posts (still posted)")

	code, resp = postForm(t, ts, url.Values{"status": {"秘密42"}, "visibility": {"public"}})
	require.Equal(t, 200, code, resp)
	require.Equal(t, "unlisted", resp["visibility"], "regex entries match")

	code, resp = postForm(t, ts, url.Values{"status": {"only a spoiler"}, "visibility": {"public"}})
	require.Equal(t, 200, code, resp)
	require.Equal(t, "public", resp["visibility"], "every AND part must be present")

	// Misskey checks the CW instead of the text when a CW is present.
	code, resp = postForm(t, ts, url.Values{"status": {"spoiler alert"}, "spoiler_text": {"cw"}, "visibility": {"public"}})
	require.Equal(t, 200, code, resp)
	require.Equal(t, "public", resp["visibility"])

	code, resp = postForm(t, ts, url.Values{"status": {"spoiler alert"}, "visibility": {"private"}})
	require.Equal(t, 200, code, resp)
	require.Equal(t, "private", resp["visibility"], "narrower visibilities are kept")
}

func TestCreateProhibitedWordRejectsPost(t *testing.T) {
	ts := newTestSetup(t)
	withModerationWords(t, nil, []string{"forbidden", "/casino\\s+bonus/i"})

	code, resp := postForm(t, ts, url.Values{"status": {"this is forbidden"}, "visibility": {"public"}})
	require.Equal(t, 422, code, resp)
	require.Equal(t, "PROHIBITED_WORDS", resp["code"])

	code, _ = postForm(t, ts, url.Values{"status": {"Get your CASINO   Bonus"}, "visibility": {"direct"}})
	require.Equal(t, 422, code, "regex with flags; applies to every visibility")

	code, _ = postForm(t, ts, url.Values{"status": {"poll"}, "visibility": {"public"}, "poll[options][]": {"ok", "forbidden"}, "poll[expires_in]": {"3600"}})
	require.Equal(t, 422, code, "poll choices are checked")

	code, resp = postForm(t, ts, url.Values{"status": {"all fine"}, "visibility": {"public"}})
	require.Equal(t, 200, code, resp)
}
