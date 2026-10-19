package datatransfer

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// randomReplyForest builds reply trees mixing local and remote authors, all
// visibilities, direct parents, archive-only parents and replies spooled
// before their parents.
func randomReplyForest(t *testing.T, seed int64, n int) *Bundle {
	rng := rand.New(rand.NewSource(seed))
	b := streamFixture(t, 3, 4)
	at := b.Posts[0].CreatedAt
	vis := []string{"public", "unlisted", "followers", "direct"}
	authors := []string{"u0", "u1", "u2", "r1"}
	var ids []string
	var posts []Post
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("f%03d", i)
		author := authors[rng.Intn(len(authors))]
		p := Post{ID: id, AuthorID: author, Visibility: vis[rng.Intn(len(vis))], Text: id, CreatedAt: at.Add(time.Duration(i+1) * time.Second)}
		if author == "r1" {
			p.URI = "https://remote.example/notes/" + id
		} else {
			p.URI = "https://site.example/notes/" + id
		}
		if p.Visibility == "direct" {
			p.RecipientIDs = []string{authors[rng.Intn(3)]}
		}
		if len(ids) > 0 && rng.Intn(4) > 0 {
			p.ReplyID = ids[rng.Intn(len(ids))]
		}
		if rng.Intn(12) == 0 {
			p.ArchiveOnly = true
		}
		ids = append(ids, id)
		posts = append(posts, p)
	}
	// Spool order differs from creation order: children may precede parents.
	rng.Shuffle(len(posts), func(a, c int) { posts[a], posts[c] = posts[c], posts[a] })
	b.Posts = append(b.Posts, posts...)
	return b
}

func TestNarrowedReplyPreviewCountEqualsImport(t *testing.T) {
	for seed := int64(1); seed <= 12; seed++ {
		for _, chunk := range []int{1, 3, 7, 1000} {
			t.Run(fmt.Sprintf("seed%d/chunk%d", seed, chunk), func(t *testing.T) {
				b := randomReplyForest(t, seed, 40)
				o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
				conn := importerDB(t)
				sp := spoolBundle(t, b)
				count := func(r *Report) int {
					for _, is := range r.Issues {
						if is.Code == ReplyNarrowedCode {
							return is.Count
						}
					}
					return 0
				}
				preview, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: chunk})
				require.NoError(t, err)
				require.True(t, preview.CanExecute, "%+v", preview.Issues)
				report, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: chunk, Apply: true})
				require.NoError(t, err, "%+v", report)
				var statuses []models.Status
				require.NoError(t, conn.Find(&statuses).Error)
				narrowed := 0
				for _, s := range statuses {
					if _, ok := s.Properties["import_visibility_narrowed_from"]; ok {
						narrowed++
					}
				}
				require.Equal(t, narrowed, count(preview), "preview must predict every narrowed reply")
				require.Equal(t, narrowed, count(report), "import report must count every narrowed reply")
			})
		}
	}
}

// A resumed run (earlier partial import in the target) must still predict
// exactly the replies the import narrows.
func TestNarrowedReplyPreviewCountEqualsImportOnResume(t *testing.T) {
	for seed := int64(1); seed <= 12; seed++ {
		for _, chunk := range []int{1, 5, 1000} {
			t.Run(fmt.Sprintf("seed%d/chunk%d", seed, chunk), func(t *testing.T) {
				b := randomReplyForest(t, seed, 40)
				o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
				conn := importerDB(t)
				// First run: only posts whose ancestors are all in the prefix.
				first := *b
				first.Posts = nil
				in := map[string]bool{}
				for _, p := range b.Posts {
					if p.ID[0] != 'f' || (p.ReplyID == "" || in[p.ReplyID]) && len(first.Posts) < len(b.Posts)/2 {
						in[p.ID] = true
						first.Posts = append(first.Posts, p)
					}
				}
				r1, err := ImportSpool(context.Background(), conn, spoolBundle(t, &first), o, StreamOptions{ChunkSize: chunk, Apply: true})
				require.NoError(t, err, "%+v", r1)
				narrowedNow := func() int {
					var statuses []models.Status
					require.NoError(t, conn.Find(&statuses).Error)
					n := 0
					for _, s := range statuses {
						if _, ok := s.Properties["import_visibility_narrowed_from"]; ok {
							n++
						}
					}
					return n
				}
				before := narrowedNow()
				sp := spoolBundle(t, b)
				preview, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: chunk})
				require.NoError(t, err)
				require.True(t, preview.CanExecute, "%+v", preview.Issues)
				report, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: chunk, Apply: true})
				require.NoError(t, err, "%+v", report)
				count := func(r *Report) int {
					for _, is := range r.Issues {
						if is.Code == ReplyNarrowedCode {
							return is.Count
						}
					}
					return 0
				}
				require.Equal(t, narrowedNow()-before, count(preview))
				require.Equal(t, narrowedNow()-before, count(report))
			})
		}
	}
}
