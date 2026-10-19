package datatransfer

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// Misskey stores local custom emoji reactions as ":name@.:"; they must become
// live reactions to the imported local emoji, not private archive records.
func TestImportMisskeyLocalEmojiReactions(t *testing.T) {
	for _, streamed := range []bool{false, true} {
		b := streamFixture(t, 2, 4)
		b.Emojis = []Emoji{{ID: "e1", Shortcode: "blob", URL: "https://files.site.example/blob.png"}}
		b.Reactions = []Reaction{{ID: "r1", AccountID: "u0", PostID: "p0002", Emoji: ":blob@.:"}, {ID: "r2", AccountID: "u1", PostID: "p0002", Emoji: "👍"}}
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
		var rows []models.StatusReaction
		require.NoError(t, conn.Order("name").Find(&rows).Error)
		require.Len(t, rows, 2, "streamed=%v archived=%v", streamed, report.Archived)
		require.Equal(t, ":blob:", rows[0].Name)
		require.NotZero(t, rows[0].EmojiID)
	}
}
