package moderation

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestWordMatcherMisskeySyntax(t *testing.T) {
	m := CompileWords([]string{"", "apple banana", "/^ab+c$/m", "/HELLO/i", "/bad(/", "/x/q"})
	require.True(t, m.Match("banana and apple"), "space-separated words are AND-ed, order free")
	require.False(t, m.Match("only apple"))
	require.True(t, m.Match("line\nabbbc\nend"), "m flag")
	require.True(t, m.Match("well hello there"), "i flag")
	require.False(t, m.Match("Apple Banana"), "plain words are case-sensitive like String.includes")
	require.False(t, m.Match("bad("), "invalid regex never matches")
	require.False(t, m.Match("x"), "unknown flag invalidates the regex")
	require.False(t, m.Match(""), "empty text never matches")
	require.True(t, CompileWords(nil).Empty())

	_, ok := ValidateWords([]string{"/ok/", "plain"})
	require.True(t, ok)
	bad, ok := ValidateWords([]string{"/[/"})
	require.False(t, ok)
	require.Equal(t, "/[/", bad)
}

func TestNoteTextForKeywordCheck(t *testing.T) {
	require.Equal(t, "cwtext\na\nb\n", NoteTextForKeywordCheck("cw", "text", []string{"a", "b"}, nil))
}

func snapshot(fields func(*Snapshot)) *Snapshot {
	s := &Snapshot{UGCVisibilityForVisitor: UGCAll}
	fields(s)
	s.compile()
	return s
}

func TestHostMatchingIncludesSubdomains(t *testing.T) {
	s := snapshot(func(s *Snapshot) {
		s.BlockedHosts = NormalizeHosts([]string{"Bad.Example", "https://evil.test/path", "*.wild.test"}, nil)
		s.SilencedHosts = NormalizeHosts([]string{"quiet.example", "bad.example"}, s.BlockedHosts)
		s.MediaSilencedHosts = NormalizeHosts([]string{"media.example"}, s.BlockedHosts)
	})
	require.Equal(t, []string{"bad.example", "evil.test", "wild.test"}, s.BlockedHosts)
	require.Equal(t, []string{"quiet.example"}, s.SilencedHosts, "silenced list excludes blocked hosts")

	require.True(t, s.IsBlockedHost("bad.example"))
	require.True(t, s.IsBlockedHost("sub.BAD.example"), "subdomains are blocked")
	require.True(t, s.IsBlockedHost("a.b.wild.test"))
	require.False(t, s.IsBlockedHost("notbad.example"), "suffix must be a whole label")
	require.False(t, s.IsBlockedHost(""))

	require.True(t, s.IsSilencedHost("x.quiet.example"))
	require.True(t, s.IsMediaSilencedHost("MEDIA.example"))
	require.False(t, s.IsMediaSilencedHost("sub.media.example"), "media silence is exact-host like Misskey")

	set := models.HostSet{"example.org": true}
	require.True(t, set.Contains("deep.sub.example.org."))
	require.False(t, set.Contains("example.org.evil"))
}

func TestApplyNotePolicy(t *testing.T) {
	s := snapshot(func(s *Snapshot) {
		s.SensitiveWords = []string{"nsfw"}
		s.ProhibitedWords = []string{"spam link"}
		s.SilencedHosts = []string{"quiet.example"}
	})
	v, err := s.ApplyNotePolicy(Note{Text: "nsfw pic"}, models.StatusVisibilityPublic)
	require.NoError(t, err)
	require.Equal(t, uint8(models.StatusVisibilityUnlisted), v)

	v, err = s.ApplyNotePolicy(Note{CW: "warning", Text: "nsfw pic"}, models.StatusVisibilityPublic)
	require.NoError(t, err)
	require.Equal(t, uint8(models.StatusVisibilityPublic), v, "cw is checked instead of text")

	_, err = s.ApplyNotePolicy(Note{CW: "spam", Text: " link"}, models.StatusVisibilityPrivate)
	require.ErrorIs(t, err, ErrProhibitedWords, "cw and text are concatenated")

	_, err = s.ApplyNotePolicy(Note{Text: "hi", PollChoices: []string{"spam link"}, Host: "remote.example"}, models.StatusVisibilityPublic)
	require.ErrorIs(t, err, ErrProhibitedWords, "remote notes are rejected too")

	v, err = s.ApplyNotePolicy(Note{Text: "hello", Host: "sub.quiet.example"}, models.StatusVisibilityPublic)
	require.NoError(t, err)
	require.Equal(t, uint8(models.StatusVisibilityUnlisted), v, "public notes from silenced hosts become home")

	v, err = s.ApplyNotePolicy(Note{Text: "hello"}, models.StatusVisibilityPublic)
	require.NoError(t, err)
	require.Equal(t, uint8(models.StatusVisibilityPublic), v)
}

func TestNamesAndTags(t *testing.T) {
	s := snapshot(func(s *Snapshot) {
		s.PreservedUsernames = DefaultPreservedUsernames
		s.ProhibitedWordsForNameOfUser = []string{"official"}
		s.HiddenTags = []string{"#Secret", "hidden"}
	})
	require.ErrorIs(t, s.CheckLocalNames("Admin", "", true), ErrPreservedUsername)
	require.NoError(t, s.CheckLocalNames("Admin", "", false))
	require.ErrorIs(t, s.CheckLocalNames("alice", "the official one", true), ErrProhibitedName)
	require.NoError(t, s.CheckLocalNames("alice", "Alice", true))
	require.True(t, s.IsHiddenTag("secret"))
	require.True(t, s.IsHiddenTag("#HIDDEN"))
	require.False(t, s.IsHiddenTag("public"))
}

func TestGuestVisibility(t *testing.T) {
	for mode, want := range map[string][2]bool{UGCAll: {true, true}, UGCLocal: {true, false}, UGCNone: {false, false}} {
		s := snapshot(func(s *Snapshot) { s.UGCVisibilityForVisitor = mode })
		require.Equal(t, want[0], s.GuestCanSee(""), mode)
		require.Equal(t, want[1], s.GuestCanSee("remote.example"), mode)
	}
	require.Equal(t, UGCAll, NormalizeUGC("bogus"))
}

func TestModeratorsInactive(t *testing.T) {
	now := time.Now()
	require.False(t, ModeratorsInactive(nil, now), "no moderators with activity: nothing to judge")
	require.True(t, ModeratorsInactive([]time.Time{now.Add(-8 * 24 * time.Hour)}, now))
	require.False(t, ModeratorsInactive([]time.Time{now.Add(-8 * 24 * time.Hour), now.Add(-time.Hour)}, now), "one active moderator keeps registration open")
}

func TestUpdateChangesNormalisesAndValidates(t *testing.T) {
	blocked := []string{"B.example", "b.example", ""}
	silenced := []string{"b.example", "c.example"}
	words := []string{"ok", "/(/"}
	_, err := Update{SensitiveWords: &words}.Changes()
	require.Error(t, err)
	ch, err := Update{BlockedHosts: &blocked, SilencedHosts: &silenced}.Changes()
	require.NoError(t, err)
	require.Equal(t, []string{"b.example"}, ch[KeyBlockedHosts])
	require.Equal(t, []string{"c.example"}, ch[KeySilencedHosts])
	bad := "everyone"
	_, err = Update{UGCVisibilityForVisitor: &bad}.Changes()
	require.Error(t, err)
}

func TestAutoCloseRegistrationToggle(t *testing.T) {
	now := time.Now()
	stale := []time.Time{now.Add(-8 * 24 * time.Hour)}
	require.True(t, ShouldAutoCloseRegistration(true, true, stale, now))
	require.False(t, ShouldAutoCloseRegistration(true, false, stale, now), "toggle off: never auto-close")
	require.False(t, ShouldAutoCloseRegistration(false, true, stale, now), "already closed")
	require.False(t, ShouldAutoCloseRegistration(true, true, []time.Time{now.Add(-6 * 24 * time.Hour)}, now))
}

func TestAutoCloseRegistrationDefaultsOn(t *testing.T) {
	var u Update
	require.NoError(t, json.Unmarshal([]byte(`{}`), &u))
	require.Nil(t, u.AutoCloseRegistration, "absent field leaves the stored value (default on) untouched")
}

func TestScheduledStatusPolicyAtPublish(t *testing.T) {
	restore := SetForTesting(Snapshot{SensitiveWords: []string{"spoiler"}, ProhibitedWords: []string{"casino"}})
	defer restore()
	require.NotNil(t, models.ScheduledPublishPolicy, "hook installed")
	s := &models.Status{Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "<p>a spoiler</p>"}}
	require.NoError(t, CheckScheduledStatus(s))
	require.EqualValues(t, models.StatusVisibilityUnlisted, s.Visibility)
	bad := &models.Status{Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "ok"},
		Poll: &models.Poll{Options: models.PollOptions{{Content: "casino"}}}}
	require.ErrorIs(t, CheckScheduledStatus(bad), ErrProhibitedWords)
	require.ErrorIs(t, bad.Public(nil), ErrProhibitedWords, "Public refuses before touching the DB")
}
