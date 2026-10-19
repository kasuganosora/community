package scripts

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// The rehearsal-round-2 failure on every engine (SQLite, PostgreSQL via
// TEST_DATABASE_URL, MariaDB/MySQL via COMMUNITY_MIGRATION_TEST_MYSQL_DSN):
// a reply wider than its followers-only parent is reported by preflight and
// imported narrowed, also when the run resumes at the posts stage.
func TestReplyNarrowingOnEveryEngine(t *testing.T) {
	conn := widthTestDB(t)
	at := time.Now().Add(-time.Hour)
	b := &datatransfer.Bundle{Schema: datatransfer.Schema, Scope: "site", Source: datatransfer.Source{Platform: "misskey", BaseURL: "https://site.example"}}
	b.Accounts = []datatransfer.Account{
		{ID: "r1", Username: "alice", Domain: "remote.example", ActorURI: "https://remote.example/users/r1", CreatedAt: at},
		{ID: "r2", Username: "bob", Domain: "other.example", ActorURI: "https://other.example/users/r2", CreatedAt: at},
	}
	b.Posts = []datatransfer.Post{
		{ID: "pf", AuthorID: "r1", Visibility: "followers", URI: "https://remote.example/notes/pf", Text: "followers only", CreatedAt: at.Add(time.Second)},
		{ID: "pr", AuthorID: "r2", Visibility: "unlisted", ReplyID: "pf", URI: "https://other.example/notes/pr", Text: "home reply", CreatedAt: at.Add(2 * time.Second)},
		{ID: "pq", AuthorID: "r1", Visibility: "public", ReplyID: "pr", URI: "https://remote.example/notes/pq", Text: "chain", CreatedAt: at.Add(3 * time.Second)},
	}
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	sp, err := datatransfer.OpenSpool(filepath.Join(t.TempDir(), "spool.sqlite3"))
	require.NoError(t, err)
	defer sp.Close()
	_, err = sp.ReadBundleJSON(context.Background(), bytes.NewReader(raw), nil)
	require.NoError(t, err)
	o := datatransfer.Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}

	narrowed := func(r *datatransfer.Report) datatransfer.Issue {
		for _, is := range r.Issues {
			if is.Code == datatransfer.ReplyNarrowedCode {
				return is
			}
		}
		t.Fatalf("no %s issue in %+v", datatransfer.ReplyNarrowedCode, r.Issues)
		return datatransfer.Issue{}
	}
	preview, err := datatransfer.ImportSpool(context.Background(), conn, sp, o, datatransfer.StreamOptions{ChunkSize: 1})
	require.NoError(t, err)
	require.True(t, preview.CanExecute, "%+v", preview.Issues)
	require.Equal(t, 2, narrowed(preview).Count)
	require.Equal(t, []string{"pq", "pr"}, narrowed(preview).SampleIDs)

	ctx, cancel := context.WithCancel(context.Background())
	_, err = datatransfer.ImportSpool(ctx, conn, sp, o, datatransfer.StreamOptions{ChunkSize: 1, Apply: true, OnProgress: func(p datatransfer.StreamProgress) {
		if p.Stage == "posts" && p.Done >= 1 {
			cancel()
		}
	}})
	require.Error(t, err)
	report, err := datatransfer.ImportSpool(context.Background(), conn, sp, o, datatransfer.StreamOptions{ChunkSize: 1, Apply: true})
	require.NoError(t, err, "%+v", report)
	var statuses []models.Status
	require.NoError(t, conn.Order("id").Find(&statuses).Error)
	require.Len(t, statuses, 3)
	for _, s := range statuses {
		require.EqualValues(t, models.StatusVisibilityPrivate, s.Visibility, "no reply wider than its followers-only parent")
	}
}
