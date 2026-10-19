package datatransfer

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// Round 3: polls were exported with the author's userId as id, so several
// polls of one author shared a source id and the preserved stage hit
// "import conflict for archive:<misskey:poll>". Such a spool must be refused
// at validate time, with samples.
func TestDuplicateSourceIDsBlockSpool(t *testing.T) {
	b := streamFixture(t, 3, 6)
	b.Records = []PreservedRecord{
		{Kind: "misskey:poll", ID: "u1", OwnerID: "u1", Data: json.RawMessage(`{"noteId":"p0001"}`)},
		{Kind: "misskey:poll", ID: "u1", OwnerID: "u1", Data: json.RawMessage(`{"noteId":"p0002"}`)},
		{Kind: "misskey:poll_vote", ID: "v1", Data: json.RawMessage(`{}`)},
	}
	// A multiple-choice vote is two rows for one (poll, account): not a duplicate.
	b.Polls = []Poll{{ID: "p0001", PostID: "p0001", Multiple: true, Options: []PollOption{{Title: "a"}, {Title: "b"}}}}
	b.PollVotes = []PollVote{{PollID: "p0001", AccountID: "u2", Option: 0}, {PollID: "p0001", AccountID: "u2", Option: 1}}
	sp := spoolBundle(t, b)
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}

	dups, err := sp.DuplicateSourceIDs()
	require.NoError(t, err)
	require.Len(t, dups, 1, "%+v", dups)
	require.Equal(t, DuplicateSourceIDCode, dups[0].Code)
	require.Equal(t, "records:misskey:poll", dups[0].Kind)
	require.Equal(t, []string{"u1"}, dups[0].SampleIDs)

	conn := importerDB(t)
	preview, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 2})
	require.NoError(t, err)
	require.False(t, preview.CanExecute)
	_, err = ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 2, Apply: true})
	require.ErrorContains(t, err, "duplicate source ids")
}

func TestArchivedRecordsAreBrokenDownByReason(t *testing.T) {
	b := streamFixture(t, 3, 6)
	at := b.Posts[len(b.Posts)-1].CreatedAt
	b.Posts = append(b.Posts,
		Post{ID: "q1", AuthorID: "u0", Visibility: "public", ReblogID: "p0000", Text: "quote", ArchiveOnly: true, CreatedAt: at.Add(1e9), URI: "https://site.example/notes/q1",
			Extensions: json.RawMessage(`{"format":"mfm","source":{"renoteId":"p0000","visibility":"public"}}`)},
		Post{ID: "c1", AuthorID: "u0", Visibility: "public", ArchiveOnly: true, CreatedAt: at.Add(2e9), URI: "https://site.example/notes/c1",
			Extensions: json.RawMessage(`{"format":"mfm","source":{"channelId":"ch","visibility":"public"}}`)},
		Post{ID: "b1", AuthorID: "u1", Visibility: "public", ReblogID: "c1", CreatedAt: at.Add(3e9), URI: "https://site.example/notes/b1"},
	)
	b.Reactions = append(b.Reactions, Reaction{ID: "rx", AccountID: "u2", PostID: "c1", Emoji: "👍"})
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	report, err := ImportSpool(context.Background(), importerDB(t), spoolBundle(t, b), o, StreamOptions{ChunkSize: 3, Apply: true})
	require.NoError(t, err, "%+v", report)
	got := map[string]Issue{}
	for _, is := range report.Issues {
		if is.Code == ArchivedRecordsCode {
			got[is.Kind] = is
		}
	}
	require.Equal(t, 1, got["post:quote_note"].Count, "%+v", got)
	require.Equal(t, []string{"q1"}, got["post:quote_note"].SampleIDs)
	require.Equal(t, 1, got["post:channel_note"].Count)
	require.Equal(t, 1, got["post:boost_of_archived_post"].Count)
	require.Equal(t, []string{"rx"}, got["reaction:post_not_published"].SampleIDs)
	sum := 0
	for k, is := range got {
		if k[:5] == "post:" {
			sum += is.Count
		}
	}
	require.Equal(t, report.Archived["post"], sum, "every archived post has a reason")
}
