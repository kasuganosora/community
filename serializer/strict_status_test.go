package serializer

import (
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func strictDB(t *testing.T) *gorm.DB {
	t.Helper()
	withTestDBMigrated(t, &models.Account{}, &models.AccountExtra{}, &models.Status{}, &models.StatusContent{}, &models.Block{}, &models.Follow{},
		&models.StatusToAccount{}, &models.Filter{}, &models.MuteAccount{}, &models.MuteStatus{}, &models.PinStatus{}, &models.FavoriteStatus{},
		&models.BookmarkStatus{}, &models.PollOptionVote{}, &models.StatusReaction{}, &models.Emoji{}, &models.AccountEmoji{},
		&models.SystemSetting{}, &models.DomainBlock{})

	return db.GetDB().Session(&gorm.Session{SkipHooks: true})
}

func strictFixtures() models.Statuses {
	created := time.Date(2026, 10, 9, 1, 2, 3, 456789000, time.UTC)
	edited := created.Add(time.Hour)
	expires := created.Add(24 * time.Hour)
	remoteDomain := "remote.example"
	w, h := 1200, 800
	dur := 12.5
	thumb := "t.jpg"

	local := &models.Account{Id: 1, Username: "luna", CreatedAt: &created, LastStatusAt: &created}
	remote := &models.Account{Id: 2, Username: "bob", Domain: &remoteDomain, DisplayName: "Bob :blob:", Avatar: "https://remote.example/a.png", CreatedAt: &created}
	emoji := &models.Emoji{Id: 5, Shortcode: "blob", Url: "https://remote.example/blob.png", Domain: remoteDomain, VisibleInPicker: true}

	app := &models.Application{Id: 1, Name: "Toot", Website: ""}
	orig := &models.Status{Id: 10, AccountId: 1, Account: local, CreatedAt: &created, Content: models.StatusContent{Content: "hello"},
		Application: app, ToAccounts: []*models.Account{}}
	reply := &models.Status{Id: 11, AccountId: 1, Account: local, CreatedAt: &created, Content: models.StatusContent{Content: "re"},
		InReplyToId: ptr(uint64(10)), InReplyToAccountId: ptr(uint64(1)), Visibility: models.StatusVisibilityUnlisted, Language: "zh",
		Edited: true, UpdatedAt: &edited, ToAccounts: []*models.Account{local}, Tags: models.Tags{{Id: 1, Name: "#Go"}}}
	boost := &models.Status{Id: 12, AccountId: 1, Account: local, CreatedAt: &created, ReblogId: ptr(uint64(10)), Reblog: orig, ToAccounts: []*models.Account{}}

	rich := &models.Status{
		Id: 20, AccountId: 2, Account: remote, Domain: &remoteDomain, CreatedAt: &created, Sensitive: true,
		OriginUri: ptr("https://remote.example/users/bob/statuses/20"), Language: "",
		Content: models.StatusContent{Content: "<p>hi :blob:</p>", SpoilerText: "cw", Card: map[string]any{
			"url": "https://example.com/a", "title": "A", "description": "d", "type": "link", "author_name": "", "author_url": "",
			"provider_name": "", "provider_url": "", "html": "", "width": 0, "height": 0, "image": nil, "embed_url": "", "blurhash": nil,
		}},
		Emojis: models.Emojis{emoji}, ToAccounts: []*models.Account{remote},
		Attachments: models.Attachments{
			{Id: 1, FileName: "a.jpg", FilePath: "x", Type: models.MediaTypeImage, Width: &w, Height: &h, ThumbnailPath: &thumb, Blurhash: "LEHV6nWB2yk8", Description: "d", FocusX: 0.5, FocusY: -0.2},
			{Id: 2, RemoteUrl: "https://remote.example/v.mp4", Type: models.MediaTypeVideo, Duration: &dur},
			{Id: 3, RemoteUrl: "https://remote.example/x.bin", Type: "weird"},
		},
		Poll: &models.Poll{Id: 3, StatusId: 20, ExpiresAt: &expires, Options: models.PollOptions{{Id: 1, PollId: 3, Index: 0, Content: "a", Votes: 2}, {Id: 2, PollId: 3, Index: 1, Content: "b"}}},
	}
	remoteBoost := &models.Status{Id: 21, AccountId: 1, Account: local, CreatedAt: &created, ReblogId: ptr(uint64(20)), Reblog: rich, ToAccounts: []*models.Account{}}
	plainRemote := &models.Status{Id: 22, AccountId: 2, Account: remote, Domain: &remoteDomain, CreatedAt: &created, Language: "", ToAccounts: []*models.Account{},
		OriginUri: ptr("https://remote.example/users/bob/statuses/22"), Content: models.StatusContent{Content: "<p>x</p>"}}

	return models.Statuses{orig, reply, boost, remoteBoost, plainRemote}
}

func ptr[T any](v T) *T { return &v }

func TestStatusesMatchStrictMastodonSchema(t *testing.T) {
	strictDB(t)

	viewer := &models.Account{Id: 1, Username: "luna"}
	out, err := ToStatuses(strictFixtures(), viewer)
	require.NoError(t, err)
	require.Len(t, out, 5)

	for _, s := range out {
		requireValid(t, "status#"+s.Id, s, statusSchema(1))
	}

	// anonymous viewer too
	out, err = ToStatuses(strictFixtures(), nil)
	require.NoError(t, err)

	for _, s := range out {
		requireValid(t, "anon status#"+s.Id, s, statusSchema(1))
	}
}

// The checker itself must reject the divergences that broke Toot!.
func TestStrictCheckerRejectsKnownBadShapes(t *testing.T) {
	bad := func(mutate func(m map[string]any)) []string {
		m := map[string]any{"id": "1", "uri": "https://a/b", "url": "https://a/b", "created_at": "2026-10-09T01:02:03.456Z"}
		mutate(m)

		var errs []string
		check("x", m, obj(map[string]sch{"id": idS, "uri": urlS, "url": urlS, "created_at": dateS, "language": nullable(sch{k: kLang}), "tags": arr(tagS)}), &errs)

		return errs
	}

	require.Empty(t, bad(func(m map[string]any) { m["language"] = nil; m["tags"] = []any{} }))
	require.NotEmpty(t, bad(func(m map[string]any) { m["language"] = ""; m["tags"] = []any{} }), "empty language")
	require.NotEmpty(t, bad(func(m map[string]any) { m["language"] = nil; m["tags"] = nil }), "null array")
	require.NotEmpty(t, bad(func(m map[string]any) { m["language"] = nil; m["tags"] = []any{}; m["id"] = float64(1) }), "numeric id")
	require.NotEmpty(t, bad(func(m map[string]any) { m["language"] = nil; m["tags"] = []any{}; m["url"] = "" }), "empty url")
	require.NotEmpty(t, bad(func(m map[string]any) {
		m["language"] = nil
		m["tags"] = []any{}
		m["created_at"] = "2026-10-09T01:02:03.456789Z"
	}), "6-digit ms")
}

func TestOtherEntitiesMatchStrictSchema(t *testing.T) {
	requireValid(t, "relationship", ToFollowStatus(&models.Account{Id: 7}, nil), obj(map[string]sch{
		"id": idS, "following": boolS, "showing_reblogs": boolS, "notifying": boolS, "languages": nullable(arr(strS)),
		"followed_by": boolS, "blocking": boolS, "blocked_by": boolS, "muting": boolS, "muting_notifications": boolS,
		"requested": boolS, "requested_by": boolS, "domain_blocking": boolS, "endorsed": boolS, "note": strS,
	}))

	requireValid(t, "list", ToList(&models.List{Id: 3, Title: "x"}), obj(map[string]sch{"id": idS, "title": strS, "replies_policy": enum("followed", "list", "none")}))

	requireValid(t, "emoji", ToEmoji(&models.Emoji{Shortcode: "a", Url: "https://x/a.png"}), emojiS)

	requireValid(t, "filter", ToFilter(&models.Filter{Id: 4, Title: "t", FilterAction: models.FilterActionWarn}), obj(map[string]sch{
		"id": idS, "title": strS, "context": arr(strS), "expires_at": nullable(dateS),
		"filter_action": enum("warn", "hide"), "keywords": arr(sch{k: kAny}), "statuses": arr(sch{k: kAny}),
	}))

	created := time.Date(2026, 10, 9, 1, 2, 3, 456789000, time.UTC)
	n := &Notification{Id: "5", Type: "favourite", CreatedAt: NewAPITime(&created),
		Account: ToAccount(&models.Account{Id: 1, Username: "a", CreatedAt: &created})}
	requireValid(t, "notification", n, obj(map[string]sch{
		"id": idS, "type": enum("mention", "status", "reblog", "follow", "follow_request", "favourite", "poll", "update", "admin.sign_up", "admin.report", "emoji_reaction"),
		"created_at": dateS, "account": accountS, "status": opt(sch{k: kAny}),
	}))
}
