package datatransfer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// quoteFixture: Misskey renotes with text. q1 quotes a remote public post
// (t1) that comes later in the spool (a later chunk); q2 quotes a post that
// was not exported (kept as a link); q3 quotes a followers-only post (never
// exposed); q4 quotes an archive-only channel note. A boost of q1 and a
// reaction on q1 import normally.
func quoteFixture(t *testing.T) *Bundle {
	b := streamFixture(t, 3, 6)
	at := b.Posts[len(b.Posts)-1].CreatedAt
	add := func(p Post) {
		at = at.Add(time.Second)
		p.CreatedAt = at
		if p.URI == "" {
			p.URI = "https://site.example/notes/" + p.ID
		}
		b.Posts = append(b.Posts, p)
	}
	add(Post{ID: "q1", AuthorID: "u0", Visibility: "public", Text: "look", QuoteID: "t1", QuoteURI: "https://remote.example/notes/t1"})
	add(Post{ID: "q2", AuthorID: "u1", Visibility: "public", Text: "gone", QuoteID: "nope", QuoteURI: "https://elsewhere.example/notes/nope"})
	add(Post{ID: "tf", AuthorID: "r1", Visibility: "followers", URI: "https://remote.example/notes/tf", Text: "secret"})
	add(Post{ID: "q3", AuthorID: "u2", Visibility: "public", Text: "leak?", QuoteID: "tf"})
	add(Post{ID: "ch", AuthorID: "u0", Visibility: "public", Text: "channel", ArchiveOnly: true, Extensions: json.RawMessage(`{"source":{"channelId":"c","visibility":"public"}}`)})
	add(Post{ID: "q4", AuthorID: "u1", Visibility: "unlisted", Text: "re channel", QuoteID: "ch"})
	add(Post{ID: "b1", AuthorID: "u2", Visibility: "public", ReblogID: "q1"})
	// The quoted remote post is spooled after its quote.
	add(Post{ID: "t1", AuthorID: "r1", Visibility: "public", URI: "https://remote.example/notes/t1", Text: "original"})
	b.Reactions = append(b.Reactions, Reaction{ID: "rq", AccountID: "u2", PostID: "q1", Emoji: "👍"})
	return b
}

func quoteCounts(r *Report) map[string]int {
	out := map[string]int{}
	for _, is := range r.Issues {
		if is.Code == QuoteUnlinkedCode {
			out[is.Kind] = is.Count
		}
	}
	return out
}

func checkQuotes(t *testing.T, conn *gorm.DB) {
	t.Helper()
	byURI := func(uri string) models.Status {
		var s models.Status
		require.NoError(t, conn.Where("local_object_uri = ? OR origin_uri = ?", uri, uri).First(&s).Error, uri)
		return s
	}
	q1, t1 := byURI("https://site.example/notes/q1"), byURI("https://remote.example/notes/t1")
	require.NotNil(t, q1.QuoteId, "quote linked across chunks")
	require.Equal(t, t1.Id, *q1.QuoteId)
	require.Nil(t, q1.ReblogId, "a quote is not a boost")
	require.NotContains(t, q1.Properties, quotePendingProperty)
	q2 := byURI("https://site.example/notes/q2")
	require.Nil(t, q2.QuoteId)
	require.Equal(t, quoteReasonMissing, q2.Properties[quoteUnlinkedProp])
	require.Equal(t, "https://elsewhere.example/notes/nope", q2.Properties[quoteURIProperty], "link fallback kept")
	q3 := byURI("https://site.example/notes/q3")
	require.Nil(t, q3.QuoteId, "followers-only target is never exposed by a quote")
	require.Equal(t, quoteReasonNotQuotable, q3.Properties[quoteUnlinkedProp])
	q4 := byURI("https://site.example/notes/q4")
	require.Nil(t, q4.QuoteId)
	require.Equal(t, quoteReasonArchived, q4.Properties[quoteUnlinkedProp])
	b1 := byURI("https://site.example/notes/b1")
	require.NotNil(t, b1.ReblogId, "boost of a quote is no longer archived")
	require.Equal(t, q1.Id, *b1.ReblogId)
	var reactions int64
	require.NoError(t, conn.Model(&models.StatusReaction{}).Where("status_id = ?", q1.Id).Count(&reactions).Error)
	require.EqualValues(t, 1, reactions, "reaction on a quote is imported")
	var pending int64
	require.NoError(t, conn.Model(&models.Status{}).Where(dialect.JSONText(conn, "properties", quotePendingProperty)+" IS NOT NULL").Count(&pending).Error)
	require.Zero(t, pending)
}

func TestQuotesStreamedLinkedAcrossChunks(t *testing.T) {
	for _, chunk := range []int{1, 2, 1000} {
		b := quoteFixture(t)
		o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
		conn := importerDB(t)
		sp := spoolBundle(t, b)
		preview, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: chunk})
		require.NoError(t, err)
		require.True(t, preview.CanExecute, "%+v", preview.Issues)
		want := map[string]int{"post:" + quoteReasonMissing: 1, "post:" + quoteReasonNotQuotable: 1, "post:" + quoteReasonArchived: 1}
		require.Equal(t, want, quoteCounts(preview), "chunk %d preview", chunk)
		report, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: chunk, Apply: true})
		require.NoError(t, err, "%+v", report)
		require.Equal(t, want, quoteCounts(report), "chunk %d: preview predicts the import", chunk)
		for _, is := range report.Issues {
			if is.Code == ArchivedRecordsCode {
				require.NotContains(t, []string{"post:quote_note", "post:boost_of_archived_post", "reaction:post_not_published"}, is.Kind, "%+v", is)
			}
		}
		checkQuotes(t, conn)
		// A rerun is idempotent and leaves nothing pending.
		again, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: chunk, Apply: true})
		require.NoError(t, err, "%+v", again)
		checkQuotes(t, conn)
	}
}

func TestQuotesWholeBundleImport(t *testing.T) {
	b := quoteFixture(t)
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	conn := importerDB(t)
	report, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err, "%+v", report.Issues)
	require.Equal(t, map[string]int{"post:" + quoteReasonMissing: 1, "post:" + quoteReasonNotQuotable: 1, "post:" + quoteReasonArchived: 1}, quoteCounts(report))
	checkQuotes(t, conn)
}
