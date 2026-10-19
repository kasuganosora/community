package datatransfer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// Misskey profile extensions land in their Community columns for local and
// remote accounts, in whole-bundle and streamed imports.
func TestImportMisskeyProfileExtensions(t *testing.T) {
	verified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, streamed := range []bool{false, true} {
		b := streamFixture(t, 2, 3)
		l := &b.Accounts[0]
		l.IsCat, l.Location, l.Birthday, l.Language, l.FollowedMessage = true, "Tokyo", "2000-02-29", "ja-JP", "thanks for following"
		l.MovedTo, l.AlsoKnownAs = "https://new.example/users/x", []string{"https://old.example/users/x", "javascript:alert(1)"}
		l.Fields = []Field{{Name: "web", Value: "https://me.example", VerifiedAt: &verified}, {Name: "plain", Value: "x"}}
		r := &b.Accounts[2]
		require.Equal(t, "r1", r.ID)
		r.IsCat, r.Location, r.Birthday, r.FollowedMessage = true, strings.Repeat("地", 200), "not-a-date", strings.Repeat("m", 300)
		r.MovedTo, r.AlsoKnownAs = "https://moved.example/users/r", []string{"https://alias.example/users/r"}
		b.Emojis = []Emoji{{ID: "e1", Shortcode: "local_only", URL: "https://files.site.example/e.png", LocalOnly: true, Category: "c", Aliases: []string{"a"}, License: "CC0"}}
		o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
		conn := importerDB(t)
		var report *Report
		var err error
		if streamed {
			report, err = ImportSpool(context.Background(), conn, spoolBundle(t, b), o, StreamOptions{ChunkSize: 2, Apply: true})
		} else {
			report, err = Import(context.Background(), conn, b, o, nil)
		}
		require.NoError(t, err, "%+v", report)

		var local, remote models.Account
		require.NoError(t, conn.Where("local_actor_uri = ?", l.ActorURI).First(&local).Error)
		require.NoError(t, conn.Where("origin_id = ?", r.ActorURI).First(&remote).Error)
		require.True(t, local.IsCat)
		require.Equal(t, "Tokyo", local.Location)
		require.Equal(t, "2000-02-29", local.Birthday)
		require.Equal(t, "ja-JP", local.Language)
		require.Equal(t, "thanks for following", local.FollowedMessage)
		require.Equal(t, "https://new.example/users/x", local.MovedTo)
		require.Equal(t, []string{"https://old.example/users/x"}, local.AlsoKnownAs)

		require.True(t, remote.IsCat)
		require.Equal(t, 128, len([]rune(remote.Location)), "truncated to the API limit")
		require.Empty(t, remote.Birthday, "invalid date dropped")
		require.Len(t, remote.FollowedMessage, 200)
		require.Equal(t, "https://moved.example/users/r", remote.MovedTo)
		require.Equal(t, []string{"https://alias.example/users/r"}, remote.AlsoKnownAs)

		var extra models.AccountExtra
		require.NoError(t, conn.First(&extra, "account_id = ?", local.Id).Error)
		require.Len(t, extra.Fields, 2)
		require.NotNil(t, extra.Fields[0].VerifiedAt)
		require.True(t, extra.Fields[0].VerifiedAt.Equal(verified))
		require.Nil(t, extra.Fields[1].VerifiedAt)

		var emoji models.Emoji
		require.NoError(t, conn.Where("shortcode = ?", "local_only").First(&emoji).Error)
		require.True(t, emoji.LocalOnly)
		require.Equal(t, []string{"a"}, emoji.Aliases)
		require.Equal(t, "CC0", emoji.License)

		codes := map[string]int{}
		for _, is := range report.Issues {
			if is.SourceID == "r1" || is.SourceID == "u0" {
				codes[is.Code+"/"+is.Kind]++
			}
		}
		require.Equal(t, map[string]int{"profile_value_truncated/account.location": 1, "profile_value_dropped/account.birthday": 1,
			"profile_value_truncated/account.followed_message": 1, "profile_value_dropped/account.also_known_as": 1}, codes, "streamed=%v", streamed)
	}
}
