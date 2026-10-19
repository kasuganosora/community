package datatransfer

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// narrowFixture adds the maid.lat shape to a stream fixture: a remote
// followers-only post (rf), a local unlisted ("home") reply to it (n1), a
// public reply to that reply (n2, chain), and a remote direct post (rd) to
// u0 answered by u1 publicly while mentioning u2 and r1 (n3).
func narrowFixture(t *testing.T) *Bundle {
	b := streamFixture(t, 3, 6)
	base := "https://site.example"
	at := b.Posts[len(b.Posts)-1].CreatedAt
	add := func(p Post) {
		at = at.Add(1e9)
		p.CreatedAt = at
		b.Posts = append(b.Posts, p)
	}
	add(Post{ID: "rf", AuthorID: "r1", Visibility: "followers", URI: "https://remote.example/notes/rf", Text: "followers only"})
	add(Post{ID: "n1", AuthorID: "u0", Visibility: "unlisted", ReplyID: "rf", URI: base + "/notes/n1", Text: "home reply"})
	add(Post{ID: "n2", AuthorID: "u1", Visibility: "public", ReplyID: "n1", URI: base + "/notes/n2", Text: "reply to reply"})
	add(Post{ID: "rd", AuthorID: "r1", Visibility: "direct", RecipientIDs: []string{"u0"}, URI: "https://remote.example/notes/rd", Text: "dm"})
	add(Post{ID: "n3", AuthorID: "u0", Visibility: "public", ReplyID: "rd", RecipientIDs: []string{"u2"}, URI: base + "/notes/n3", Text: "public reply to dm"})
	return b
}

func narrowIssue(t *testing.T, r *Report) Issue {
	t.Helper()
	var found []Issue
	for _, is := range r.Issues {
		if is.Code == ReplyNarrowedCode {
			found = append(found, is)
		}
	}
	require.Len(t, found, 1, "%+v", r.Issues)
	return found[0]
}

func TestReplyWideningIsNarrowedNotFatal(t *testing.T) {
	b := narrowFixture(t)
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	conn := importerDB(t)

	preview, err := Preview(context.Background(), conn, b, o)
	require.NoError(t, err)
	require.True(t, preview.CanExecute, "narrowing is a warning, not a blocker: %+v", preview.Issues)
	is := narrowIssue(t, preview)
	require.Equal(t, "warning", is.Severity)
	require.Equal(t, 3, is.Count)
	require.ElementsMatch(t, []string{"n1", "n2", "n3"}, is.SampleIDs)

	report, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err, "%+v", report.Issues)
	require.Equal(t, 3, narrowIssue(t, report).Count, "preview and importer issues fold into one")

	status := func(uri string) models.Status {
		var s models.Status
		require.NoError(t, conn.Where("local_object_uri = ?", uri).First(&s).Error)
		return s
	}
	n1 := status("https://site.example/notes/n1")
	require.EqualValues(t, models.StatusVisibilityPrivate, n1.Visibility, "unlisted -> followers-only")
	require.Equal(t, "unlisted", n1.Properties["import_visibility_narrowed_from"])
	require.EqualValues(t, models.StatusVisibilityPrivate, status("https://site.example/notes/n2").Visibility, "chains narrow too")

	n3 := status("https://site.example/notes/n3")
	require.EqualValues(t, models.StatusVisibilityDirect, n3.Visibility)
	require.NotNil(t, n3.ConversationId)
	var recipients []uint64
	require.NoError(t, conn.Model(&models.StatusToAccount{}).Where("status_id = ?", n3.Id).Pluck("account_id", &recipients).Error)
	var r1 models.Account
	require.NoError(t, conn.Where("username = ? AND domain = ?", "remote", "remote.example").First(&r1).Error)
	require.Equal(t, []uint64{r1.Id}, recipients, "only the direct parent's participants: u2 (outside the DM) dropped, parent author added")
}

// Streamed: the warning is reported across chunks (parents in earlier
// chunks), and a run interrupted at the posts stage resumes with
// --reuse-spool semantics and narrows instead of failing.
func TestStreamedReplyNarrowingAcrossChunksAndResume(t *testing.T) {
	b := narrowFixture(t)
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	conn := importerDB(t)
	sp := spoolBundle(t, b)

	preview, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 1})
	require.NoError(t, err)
	require.True(t, preview.CanExecute, "%+v", preview.Issues)
	is := narrowIssue(t, preview)
	require.Equal(t, 3, is.Count)

	ctx, cancel := context.WithCancel(context.Background())
	_, err = ImportSpool(ctx, conn, sp, o, StreamOptions{ChunkSize: 1, Apply: true, OnProgress: func(p StreamProgress) {
		if p.Stage == "posts" && p.Done >= 2 {
			cancel()
		}
	}})
	require.ErrorIs(t, err, context.Canceled)

	report, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 1, Apply: true})
	require.NoError(t, err, "%+v", report.Issues)
	require.True(t, report.CanExecute)
	require.Equal(t, 3, narrowIssue(t, report).Count)
	var n int64
	require.NoError(t, conn.Model(&models.Status{}).Where("visibility = ? AND local_object_uri IN ?", models.StatusVisibilityPrivate,
		[]string{"https://site.example/notes/n1", "https://site.example/notes/n2"}).Count(&n).Error)
	require.EqualValues(t, 2, n)
}

// A reply cycle used to abort the whole site import; the posts stay in the
// private archive with a warning instead.
func TestSiteReplyCycleIsArchivedNotFatal(t *testing.T) {
	b := streamFixture(t, 2, 3)
	at := b.Posts[2].CreatedAt
	b.Posts = append(b.Posts,
		Post{ID: "c1", AuthorID: "u0", Visibility: "public", ReplyID: "c2", URI: "https://site.example/notes/c1", CreatedAt: at.Add(1e9)},
		Post{ID: "c2", AuthorID: "u1", Visibility: "public", ReplyID: "c1", URI: "https://site.example/notes/c2", CreatedAt: at.Add(2e9)})
	conn := importerDB(t)
	report, err := Import(context.Background(), conn, b, Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}, nil)
	require.NoError(t, err, "%+v", report.Issues)
	archived := 0
	for _, is := range report.Issues {
		if is.Code == "archived_parent" {
			archived++
		}
	}
	require.Equal(t, 2, archived)
	var n int64
	require.NoError(t, conn.Model(&models.Status{}).Where("local_object_uri IN ?", []string{"https://site.example/notes/c1", "https://site.example/notes/c2"}).Count(&n).Error)
	require.Zero(t, n)
}
