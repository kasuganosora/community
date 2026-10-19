package datatransfer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func streamFixture(t *testing.T, users, posts int) *Bundle {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	public, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	require.NoError(t, err)
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
	privatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private)}))
	password, err := bcrypt.GenerateFromPassword([]byte("fixture passphrase"), bcrypt.MinCost)
	require.NoError(t, err)
	base := "https://site.example"
	b := &Bundle{Schema: Schema, Scope: "site", Source: Source{Platform: "misskey", BaseURL: base}}
	for i := 0; i < users; i++ {
		id := fmt.Sprintf("u%d", i)
		b.Accounts = append(b.Accounts, Account{ID: id, Username: id, Local: true, ActorURI: base + "/users/" + id, AvatarID: "m" + id})
		b.Credentials = append(b.Credentials, Credential{AccountID: id, PasswordHash: string(password), PasswordScheme: "bcrypt"})
		b.Keys = append(b.Keys, FederationKey{AccountID: id, KeyID: base + "/users/" + id + "#main-key", Primary: true, PublicKey: publicPEM, PrivateKey: privatePEM})
		b.Media = append(b.Media, Media{ID: "m" + id, OwnerID: id, Name: "a.png", ContentType: "image/png", RemoteURL: "https://files.site.example/" + id + ".png", StorageReference: true, Width: 10, Height: 10})
	}
	b.Accounts = append(b.Accounts, Account{ID: "r1", Username: "remote", Domain: "remote.example", ActorURI: "https://remote.example/users/r1"})
	now := time.Now().Add(-time.Hour)
	for i := 0; i < posts; i++ {
		p := Post{ID: fmt.Sprintf("p%04d", i), AuthorID: fmt.Sprintf("u%d", i%users), Visibility: "public", Text: fmt.Sprintf("post %d", i), CreatedAt: now.Add(time.Duration(i) * time.Second)}
		p.URI = base + "/notes/" + p.ID
		if i > 0 && i%3 == 0 {
			p.ReplyID = fmt.Sprintf("p%04d", i-2)
		}
		b.Posts = append(b.Posts, p)
	}
	// Out-of-order child: a reply that appears before its parent in the stream.
	b.Posts[1].ReplyID = fmt.Sprintf("p%04d", posts-1)
	b.Relations = []Relation{{Kind: "follow", AccountID: "u0", TargetID: "u1", ShowReblogs: true}, {Kind: "follow", AccountID: "u1", TargetID: "r1", ShowReblogs: true}}
	b.Reactions = []Reaction{{ID: "fav", AccountID: "u1", PostID: "p0000"}}
	b.Bookmarks = []Bookmark{{AccountID: "u0", PostID: fmt.Sprintf("p%04d", posts-1)}}
	return b
}

func spoolBundle(t *testing.T, b *Bundle) *Spool {
	t.Helper()
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	sp, err := OpenSpool(filepath.Join(t.TempDir(), "spool.sqlite3"))
	require.NoError(t, err)
	t.Cleanup(func() { sp.Close() })
	_, err = sp.ReadBundleJSON(context.Background(), bytes.NewReader(raw), nil)
	require.NoError(t, err)
	return sp
}

func TestStreamedImportMatchesWholeBundleImport(t *testing.T) {
	b := streamFixture(t, 4, 23)
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}

	whole := importerDB(t)
	_, err := Import(context.Background(), whole, b, o, nil)
	require.NoError(t, err)

	streamed := importerDB(t)
	sp := spoolBundle(t, b)
	var stages []string
	preview, err := ImportSpool(context.Background(), streamed, sp, o, StreamOptions{ChunkSize: 3})
	require.NoError(t, err)
	require.True(t, preview.CanExecute, "%+v", preview.Issues)
	report, err := ImportSpool(context.Background(), streamed, sp, o, StreamOptions{ChunkSize: 3, Apply: true, OnProgress: func(p StreamProgress) {
		if len(stages) == 0 || stages[len(stages)-1] != p.Stage {
			stages = append(stages, p.Stage)
		}
		require.LessOrEqual(t, p.Done, p.Total)
	}})
	require.NoError(t, err, "%+v", report.Issues)
	require.True(t, report.CanExecute)
	require.Equal(t, 23, report.Created["post"])
	require.Equal(t, 5, report.Created["account"], "each account created exactly once")
	require.Contains(t, strings.Join(stages, ","), "accounts,credentials,keys,media,posts")

	for _, model := range []any{&models.Account{}, &models.User{}, &models.Status{}, &models.Follow{}, &models.FavoriteStatus{}, &models.BookmarkStatus{}, &models.MediaAttachment{}, &models.AccountKey{}, &ImportReceipt{}} {
		var a, c int64
		require.NoError(t, whole.Model(model).Count(&a).Error)
		require.NoError(t, streamed.Model(model).Count(&c).Error)
		require.Equal(t, a, c, fmt.Sprintf("%T", model))
	}
	var reply models.Status
	require.NoError(t, streamed.Where("local_object_uri = ?", "https://site.example/notes/p0001").First(&reply).Error)
	require.NotZero(t, reply.InReplyToId, "out-of-order parent resolved")

	// Rerun is idempotent: everything is skipped by receipt.
	again, err := ImportSpool(context.Background(), streamed, sp, o, StreamOptions{ChunkSize: 3, Apply: true})
	require.NoError(t, err)
	require.Empty(t, again.Created)
	var n int64
	require.NoError(t, streamed.Model(&models.Status{}).Count(&n).Error)
	require.EqualValues(t, 23, n)
}

func TestStreamedImportResumesFromCheckpoint(t *testing.T) {
	b := streamFixture(t, 3, 12)
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	conn := importerDB(t)
	sp := spoolBundle(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	_, err := ImportSpool(ctx, conn, sp, o, StreamOptions{ChunkSize: 2, Apply: true, OnProgress: func(p StreamProgress) {
		if p.Stage == "posts" && p.Done >= 4 {
			cancel()
		}
	}})
	require.ErrorIs(t, err, context.Canceled)
	require.NotEmpty(t, sp.Meta("checkpoint:posts"))
	report, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 2, Apply: true})
	require.NoError(t, err, "%+v", report.Issues)
	var n int64
	require.NoError(t, conn.Model(&models.Status{}).Count(&n).Error)
	require.EqualValues(t, 12, n)
}

func TestStreamedPreviewReportsMissingReference(t *testing.T) {
	b := streamFixture(t, 2, 4)
	b.Posts[2].MediaIDs = []string{"nope"}
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	report, err := ImportSpool(context.Background(), importerDB(t), spoolBundle(t, b), o, StreamOptions{ChunkSize: 2})
	require.NoError(t, err)
	require.False(t, report.CanExecute)
	found := false
	for _, is := range report.Issues {
		found = found || is.Code == "missing_reference"
	}
	require.True(t, found)
}

func TestSpoolNDJSONStream(t *testing.T) {
	sp, err := OpenSpool(filepath.Join(t.TempDir(), "s.sqlite3"))
	require.NoError(t, err)
	defer sp.Close()
	in := `{"header":{"schema":"community-transfer/v1","scope":"site","source":{"platform":"misskey","base_url":"https://site.example"}}}
{"s":"accounts","v":{"id":"a","username":"a","local":true,"actor_uri":"https://site.example/users/a"}}
{"s":"media","v":{"id":"m","owner_id":"a","width":1665.517,"height":10.2}}
`
	st, err := sp.ReadNDJSON(context.Background(), strings.NewReader(in), nil)
	require.NoError(t, err)
	require.Equal(t, 2, st.Records)
	require.Equal(t, "https://site.example", sp.Header.Source.BaseURL)
	_, err = sp.ReadNDJSON(context.Background(), strings.NewReader(`{"s":"bogus","v":{}}`+"\n"), nil)
	require.Error(t, err)
}

// maid.lat rehearsal: replies to archive-only parents (quotes, channel notes)
// and reactions with remote emoji outside a local-only export must not abort
// the import, in either the whole-bundle or the streamed path.
func TestImportArchivedParentsAndMissingReactionEmoji(t *testing.T) {
	for _, streamed := range []bool{false, true} {
		b := streamFixture(t, 2, 6)
		// p0001 is archive-only; p0003 replies to it.
		b.Posts[1].ArchiveOnly = true
		b.Posts[1].ReplyID = ""
		b.Posts = append(b.Posts, Post{ID: "boost", AuthorID: "u0", Visibility: "public", ReblogID: "p0001", URI: "https://site.example/notes/boost", CreatedAt: time.Now()})
		b.Reactions = append(b.Reactions, Reaction{ID: "remote-emoji", AccountID: "u1", PostID: "p0002", Emoji: ":blobcat@remote.example:"})
		o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
		conn := importerDB(t)
		var report *Report
		var err error
		if streamed {
			report, err = ImportSpool(context.Background(), conn, spoolBundle(t, b), o, StreamOptions{ChunkSize: 2, Apply: true})
		} else {
			report, err = Import(context.Background(), conn, b, o, nil)
		}
		require.NoError(t, err, "streamed=%v issues=%+v", streamed, report.Issues)
		var child models.Status
		require.NoError(t, conn.Where("local_object_uri = ?", "https://site.example/notes/p0003").First(&child).Error)
		require.Nil(t, child.InReplyToId, "streamed=%v", streamed)
		require.Equal(t, "p0001", child.Properties["import_reply_parent_archived"])
		var boosts int64
		require.NoError(t, conn.Model(&models.Status{}).Where("local_object_uri = ?", "https://site.example/notes/boost").Count(&boosts).Error)
		require.Zero(t, boosts, "a boost of archived history stays archived")
		require.GreaterOrEqual(t, report.Archived["reaction"], 1)
	}
}

func TestImportMapsRequireSigninToViewContents(t *testing.T) {
	b := streamFixture(t, 2, 5)
	b.Accounts[0].Settings = map[string]any{"requireSigninToViewContents": true}
	b.Accounts[1].Settings = map[string]any{"requireSigninToViewContents": false}
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	conn := importerDB(t)
	preview, err := Preview(context.Background(), conn, b, o)
	require.NoError(t, err)
	for _, issue := range preview.Issues {
		require.NotEqual(t, "unsupported_privacy_policy", issue.Code, issue.Message)
	}
	report, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err, "%+v", report)
	var got []models.Account
	require.NoError(t, conn.Where("domain IS NULL").Order("username").Find(&got).Error)
	flags := map[string]bool{}
	for _, a := range got {
		flags[a.Username] = a.RequireSigninToView
	}
	require.Equal(t, map[string]bool{"u0": true, "u1": false}, flags)
}

// The import report echoes source.exported_at and snapshot_id from the
// exporter header (direct mode NDJSON and bundle.json alike); it used to
// drop them, so ops read 0001-01-01 after --fetch-only + import.
func TestReportKeepsSourceExportedAt(t *testing.T) {
	sp, err := OpenSpool(filepath.Join(t.TempDir(), "s.sqlite3"))
	require.NoError(t, err)
	defer sp.Close()
	in := `{"header":{"schema":"community-transfer/v1","scope":"site","source":{"platform":"misskey","base_url":"https://site.example","snapshot_id":"keyset:2026-10-19T01:02:03.456789+00:00/2026-10-19T02:00:00+00:00","exported_at":"2026-10-19T01:02:03.456789Z"}}}
{"s":"accounts","v":{"id":"a","username":"a","local":true,"actor_uri":"https://site.example/users/a"}}
`
	_, err = sp.ReadNDJSON(context.Background(), strings.NewReader(in), nil)
	require.NoError(t, err)
	want := time.Date(2026, 10, 19, 1, 2, 3, 456789000, time.UTC)
	require.True(t, sp.Header.Source.ExportedAt.Equal(want))
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	report, err := ImportSpool(context.Background(), importerDB(t), sp, o, StreamOptions{ChunkSize: 2})
	require.NoError(t, err)
	require.True(t, report.Source.ExportedAt.Equal(want), "report source.exported_at = %v", report.Source.ExportedAt)
	require.Equal(t, "keyset:2026-10-19T01:02:03.456789+00:00/2026-10-19T02:00:00+00:00", report.Source.SnapshotID)
	out, err := json.Marshal(report)
	require.NoError(t, err)
	require.Contains(t, string(out), `"exported_at":"2026-10-19T01:02:03.456789Z"`)

	// A reopened spool (--reuse-spool) keeps the header's exported_at.
	path := sp.path
	require.NoError(t, sp.Close())
	again, err := OpenSpool(path)
	require.NoError(t, err)
	defer again.Close()
	require.True(t, again.Header.Source.ExportedAt.Equal(want))

	b := &Bundle{Source: Source{Platform: "misskey", SnapshotID: "x y;drop", ExportedAt: want}}
	r := newReport(b)
	require.Empty(t, r.Source.SnapshotID, "unsafe snapshot ids are not echoed")
	require.True(t, r.Source.ExportedAt.Equal(want))
}
