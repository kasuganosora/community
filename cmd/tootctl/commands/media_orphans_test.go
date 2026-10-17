package commands

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/matrix/homeserver"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

const testBaseURL = "https://social.example"

func setupMediaDB(t *testing.T) *gorm.DB {
	t.Helper()

	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(
		&models.Emoji{}, &models.Account{}, &models.AccountExtra{}, &models.MediaAttachment{}, &models.Follow{},
		&models.EmojiUpload{}, &models.AvatarDecoration{}, &models.Advertisement{},
		&homeserver.MatrixMedia{}, &homeserver.MatrixThumbnail{},
	))
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)

	prevURL, prevConfigured := config.C.AppBaseURL, config.AppBaseURLConfigured()
	config.SetAppBaseURL(testBaseURL, true)
	t.Cleanup(func() { config.SetAppBaseURL(prevURL, prevConfigured) })

	return conn
}

// nextAccountID: accounts.id is not an auto-increment column on SQLite.
var nextAccountID uint64 = 1000

func strPtr(s string) *string { return &s }

func seedAccount(t *testing.T, gdb *gorm.DB, username string, domain *string) *models.Account {
	t.Helper()

	acct := username
	if domain != nil {
		acct += "@" + *domain
	}

	nextAccountID++
	a := &models.Account{Id: nextAccountID, Username: username, Domain: domain, Acct: acct, DisplayName: username}
	require.NoError(t, gdb.Create(a).Error)

	return a
}

func createAttachment(t *testing.T, gdb *gorm.DB, owner uint64, filePath string, thumb *string, remoteURL string, created time.Time) *models.MediaAttachment {
	t.Helper()

	m := &models.MediaAttachment{AccountId: owner, FileName: filepath.Base(filePath), FilePath: filePath, ThumbnailPath: thumb, ContentType: "image/jpeg", Type: models.MediaTypeImage, RemoteUrl: remoteURL, CreatedAt: created}
	require.NoError(t, gdb.Create(m).Error)

	return m
}

// orphanFixture seeds a database and a backend with every kind of
// referenced file plus a few genuine orphans.
type orphanFixture struct {
	referenced []string // must survive any scan
	young      []string // unreferenced but inside the grace period
	orphans    []string // unreferenced and old: deleted with --confirm
	untouched  []string // unreferenced, old, outside the scanned namespaces
}

func seedOrphanFixture(t *testing.T, gdb *gorm.DB, backend storage.Backend, setAge func(key string, mod time.Time)) orphanFixture {
	t.Helper()

	ctx := context.Background()
	old := time.Now().Add(-72 * time.Hour)

	local := seedAccount(t, gdb, "alice", nil)
	remote := seedAccount(t, gdb, "bob", strPtr("remote.example"))

	var f orphanFixture

	// Current uploads: original + thumbnail next to it.
	createAttachment(t, gdb, local.Id, "2026/10/01/1_a.jpg", strPtr("thumb_1_a.jpg"), "", old)
	f.referenced = append(f.referenced, "media/2026/10/01/1_a.jpg", "media/2026/10/01/thumb_1_a.jpg")

	// Account avatar / header: stored as media attachments without status,
	// referenced by /media/<id>/<name> URLs.
	avatar := createAttachment(t, gdb, local.Id, "2026/10/02/2_avatar.png", strPtr("thumb_2_avatar.jpg"), "", old)
	header := createAttachment(t, gdb, local.Id, "2026/10/02/3_header.png", nil, "", old)
	require.NoError(t, gdb.Model(local).Updates(map[string]any{
		"avatar": testBaseURL + "/media/" + itoa(avatar.Id) + "/2_avatar.png",
		"header": testBaseURL + "/media/" + itoa(header.Id) + "/3_header.png",
	}).Error)
	f.referenced = append(f.referenced, "media/2026/10/02/2_avatar.png", "media/2026/10/02/thumb_2_avatar.jpg", "media/2026/10/02/3_header.png")

	// Legacy rows: full bucket URL, host path, tootctl "refreshed/" key.
	createAttachment(t, gdb, local.Id, "https://bucket.r2.example/community-file/media/2025/01/01/legacy_url.png", strPtr("thumb_legacy_url.jpg"), "", old)
	createAttachment(t, gdb, local.Id, "uploads/media/2025/01/02/legacy_path.png", nil, "", old)
	createAttachment(t, gdb, remote.Id, "refreshed/9_remote.png", nil, "https://remote.example/files/remote.png", old)
	f.referenced = append(f.referenced, "media/2025/01/01/legacy_url.png", "media/2025/01/01/thumb_legacy_url.jpg", "media/2025/01/02/legacy_path.png", "refreshed/9_remote.png")

	// Legacy avatar keys on accounts (avatars/ is never scanned anyway).
	require.NoError(t, gdb.Model(remote).Updates(map[string]any{"avatar": "avatars/bob.png", "header": "https://remote.example/header.png"}).Error)
	f.referenced = append(f.referenced, "avatars/bob.png")

	// Custom emoji and brand images.
	emojiName := "0b6c3d1e-9a7f-4c1b-8d2e-3f4a5b6c7d8e"
	require.NoError(t, gdb.Create(&models.Emoji{Shortcode: "blob", Url: testBaseURL + "/emoji-assets/" + emojiName + ".png", StaticUrl: testBaseURL + "/emoji-assets/" + emojiName + "-static.png"}).Error)
	require.NoError(t, gdb.Create(&models.AvatarDecoration{Name: "deco", ImageURL: testBaseURL + "/emoji-assets/1b6c3d1e-9a7f-4c1b-8d2e-3f4a5b6c7d8e.png"}).Error)
	f.referenced = append(f.referenced, "emojis/"+emojiName+".png", "emojis/"+emojiName+"-static.png", "emojis/1b6c3d1e-9a7f-4c1b-8d2e-3f4a5b6c7d8e.png")

	// Matrix media: original, thumbnail with full key and with bare name.
	require.NoError(t, gdb.Create(&homeserver.MatrixMedia{MediaID: "m1", ServerName: "social.example", FilePath: "matrix/2026/10/03/m1.png", CreatedAt: old}).Error)
	require.NoError(t, gdb.Create(&homeserver.MatrixThumbnail{MediaID: "m1", FilePath: "matrix/2026/10/03/thumb_m1.jpg", CreatedAt: old}).Error)
	require.NoError(t, gdb.Create(&homeserver.MatrixMedia{MediaID: "m2", ServerName: "social.example", FilePath: "matrix/2026/10/03/m2.png", CreatedAt: old}).Error)
	require.NoError(t, gdb.Create(&homeserver.MatrixThumbnail{MediaID: "m2", FilePath: "thumb_m2.jpg", CreatedAt: old}).Error)
	f.referenced = append(f.referenced, "matrix/2026/10/03/m1.png", "matrix/2026/10/03/thumb_m1.jpg", "matrix/2026/10/03/m2.png", "matrix/2026/10/03/thumb_m2.jpg")

	f.young = []string{"media/2026/10/09/fresh_upload.jpg"}
	f.orphans = []string{"media/2026/10/01/orphan.jpg", "media/2026/10/01/thumb_orphan.jpg", "matrix/2026/10/03/orphan.bin", "matrix/local/old-orphan.bin", "matrix/remote/old-orphan.bin", "matrix-media/bridge-remote/legacy-staging", "refreshed/1_gone.png"}
	f.untouched = []string{"emojis/2b6c3d1e-9a7f-4c1b-8d2e-3f4a5b6c7d8e.png", "avatars/unreferenced.png", "public/robots.txt"}

	all := append(append(append(append([]string{}, f.referenced...), f.young...), f.orphans...), f.untouched...)
	for _, key := range all {
		require.NoError(t, backend.Put(ctx, key, strings.NewReader("data:"+key), -1, ""), key)
		if setAge != nil && !slices.Contains(f.young, key) {
			setAge(key, old)
		}
	}

	return f
}

func itoa(n uint64) string { return strconv.FormatUint(n, 10) }

func requireExists(t *testing.T, backend storage.Backend, keys []string, exists bool) {
	t.Helper()

	for _, key := range keys {
		_, err := backend.Stat(context.Background(), key)
		if exists {
			require.NoError(t, err, "%s must still exist", key)
		} else {
			require.ErrorIs(t, err, storage.ErrNotFound, "%s must be deleted", key)
		}
	}
}

type backendCase struct {
	name string
	// make returns the backend and a function that backdates an object
	// (nil when the backend cannot, e.g. real S3).
	make func(t *testing.T) (storage.Backend, func(string, time.Time))
}

func orphanBackends() []backendCase {
	return []backendCase{
		{"fake-s3", func(*testing.T) (storage.Backend, func(string, time.Time)) {
			m := storagetest.NewMemory()

			return m, m.SetModTime
		}},
		{"local", func(t *testing.T) (storage.Backend, func(string, time.Time)) {
			l := &storage.LocalBackend{Root: t.TempDir()}

			return l, func(key string, mod time.Time) {
				p, err := l.Path(key)
				require.NoError(t, err)
				require.NoError(t, os.Chtimes(p, mod, mod))
			}
		}},
		{"s3", func(t *testing.T) (storage.Backend, func(string, time.Time)) {
			env := storagetest.S3FromEnv(t)

			return storage.NewS3Backend(env.Config(env.NewBucket(t))), nil
		}},
	}
}

func TestRemoveOrphansNeverDeletesReferencedFiles(t *testing.T) {
	for _, bc := range orphanBackends() {
		t.Run(bc.name, func(t *testing.T) {
			gdb := setupMediaDB(t)
			backend, setAge := bc.make(t)
			defer storage.SetDefault(backend)()

			f := seedOrphanFixture(t, gdb, backend, setAge)
			ctx := context.Background()

			now := time.Now()
			if setAge == nil {
				// Objects on a real server cannot be backdated: move the
				// clock instead and treat the "young" object as old too.
				now = now.Add(48 * time.Hour)
				f.orphans = append(f.orphans, f.young...)
				f.young = nil
			}

			// The reference set alone (without the per-object re-check)
			// must already cover every referenced file.
			refs, err := collectReferencedKeys(ctx, gdb)
			require.NoError(t, err)
			for _, key := range f.referenced {
				require.True(t, refs.has(key), "reference set misses %s", key)
			}
			for _, key := range f.orphans {
				require.False(t, refs.has(key), "orphan %s considered referenced", key)
			}

			prefixes, err := resolveOrphanPrefixes("")
			require.NoError(t, err)

			// Dry run: report only.
			var out bytes.Buffer
			require.NoError(t, runOrphanScan(ctx, &out, backend, gdb, orphanScanOptions{Prefixes: prefixes, MinAge: 24 * time.Hour, Now: now}))
			for _, key := range f.orphans {
				require.Contains(t, out.String(), "[ORPHAN] "+key)
			}
			for _, key := range append(append([]string{}, f.referenced...), f.young...) {
				require.NotContains(t, out.String(), " "+key+" (")
			}
			requireExists(t, backend, f.referenced, true)
			requireExists(t, backend, f.orphans, true)

			// Confirmed run: only the orphans go.
			out.Reset()
			require.NoError(t, runOrphanScan(ctx, &out, backend, gdb, orphanScanOptions{Prefixes: prefixes, MinAge: 24 * time.Hour, Now: now, Delete: true}))
			requireExists(t, backend, f.referenced, true)
			requireExists(t, backend, f.young, true)
			requireExists(t, backend, f.untouched, true)
			requireExists(t, backend, f.orphans, false)
			require.Contains(t, out.String(), "0 failed")
		})
	}
}

func TestRemoveOrphansRechecksRowsCreatedDuringScan(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	defer storage.SetDefault(backend)()

	ctx := context.Background()
	owner := seedAccount(t, gdb, "alice", nil)
	createAttachment(t, gdb, owner.Id, "2026/10/01/kept.jpg", nil, "", time.Now())

	for _, key := range []string{"media/2026/10/01/kept.jpg", "media/2026/10/01/late.jpg", "media/2026/10/01/thumb_late.jpg", "matrix/2026/10/01/late.png"} {
		require.NoError(t, backend.Put(ctx, key, strings.NewReader("x"), 1, ""))
		backend.SetModTime(key, time.Now().Add(-48*time.Hour))
	}

	refs, err := collectReferencedKeys(ctx, gdb)
	require.NoError(t, err)

	// Rows that appear after the reference set was loaded.
	createAttachment(t, gdb, owner.Id, "2026/10/01/late.jpg", strPtr("thumb_late.jpg"), "", time.Now())
	require.NoError(t, gdb.Create(&homeserver.MatrixMedia{MediaID: "late", ServerName: "x", FilePath: "matrix/2026/10/01/late.png"}).Error)

	res, err := scanOrphans(ctx, backend, gdb, refs, orphanScanOptions{Prefixes: orphanNamespaces, MinAge: time.Hour, Delete: true})
	require.NoError(t, err)
	require.Zero(t, res.Deleted)
	require.Len(t, backend.Keys(), 4)
}

func TestRemoveOrphansRefusesWithoutDatabaseReferences(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	defer storage.SetDefault(backend)()

	require.NoError(t, backend.Put(context.Background(), "media/2026/01/01/a.jpg", strings.NewReader("x"), 1, ""))
	backend.SetModTime("media/2026/01/01/a.jpg", time.Now().Add(-48*time.Hour))

	err := runOrphanScan(context.Background(), &bytes.Buffer{}, backend, gdb, orphanScanOptions{Prefixes: orphanNamespaces, MinAge: time.Hour, Delete: true})
	require.ErrorIs(t, err, errNoReferences)
	require.Len(t, backend.Keys(), 1)
}

func TestRemoveOrphansAbortsOnDatabaseError(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	defer storage.SetDefault(backend)()

	require.NoError(t, backend.Put(context.Background(), "media/a.jpg", strings.NewReader("x"), 1, ""))
	backend.SetModTime("media/a.jpg", time.Now().Add(-48*time.Hour))
	require.NoError(t, gdb.Migrator().DropTable(&models.MediaAttachment{}))

	err := runOrphanScan(context.Background(), &bytes.Buffer{}, backend, gdb, orphanScanOptions{Prefixes: orphanNamespaces, MinAge: time.Hour, Delete: true})
	require.Error(t, err)
	require.Len(t, backend.Keys(), 1)
}

func TestResolveOrphanPrefixes(t *testing.T) {
	got, err := resolveOrphanPrefixes("")
	require.NoError(t, err)
	require.Equal(t, orphanNamespaces, got)

	for _, ok := range []string{"media/2026/10", "/media/", "uploads/matrix/2026", "refreshed/"} {
		_, err := resolveOrphanPrefixes(ok)
		require.NoError(t, err, ok)
	}

	for _, bad := range []string{"emojis/", "avatars", "public/", "../etc", "med"} {
		_, err := resolveOrphanPrefixes(bad)
		require.Error(t, err, bad)
	}
}

func runMediaCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	app := &cli.App{Name: "tootctl", Commands: []*cli.Command{MediaCommand()}, Writer: &out, ErrWriter: &out}
	err := app.Run(append([]string{"tootctl", "media"}, args...))

	return out.String(), err
}

func TestRemoveOrphansCLIIsDryRunByDefault(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	defer storage.SetDefault(backend)()

	f := seedOrphanFixture(t, gdb, backend, backend.SetModTime)

	out, err := runMediaCLI(t, "remove-orphans")
	require.NoError(t, err)
	require.Contains(t, out, "DRY RUN")
	requireExists(t, backend, f.orphans, true)

	out, err = runMediaCLI(t, "remove-orphans", "--confirm", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "DRY RUN")
	requireExists(t, backend, f.orphans, true)

	_, err = runMediaCLI(t, "remove-orphans", "--confirm", "--min-age", "10m")
	require.ErrorContains(t, err, "--min-age")
	_, err = runMediaCLI(t, "remove-orphans", "--confirm", "--prefix", "emojis/")
	require.ErrorContains(t, err, "outside")
	requireExists(t, backend, f.orphans, true)

	out, err = runMediaCLI(t, "remove-orphans", "--confirm", "--prefix", "media/2026/10/01/")
	require.NoError(t, err)
	require.Contains(t, out, "DELETING")
	requireExists(t, backend, []string{"media/2026/10/01/orphan.jpg", "media/2026/10/01/thumb_orphan.jpg"}, false)
	requireExists(t, backend, []string{"matrix/2026/10/03/orphan.bin", "matrix/local/old-orphan.bin", "matrix/remote/old-orphan.bin", "matrix-media/bridge-remote/legacy-staging", "refreshed/1_gone.png"}, true)
	requireExists(t, backend, f.referenced, true)

	out, err = runMediaCLI(t, "remove-orphans", "--confirm")
	require.NoError(t, err)
	requireExists(t, backend, f.orphans, false)
	requireExists(t, backend, f.referenced, true)
	requireExists(t, backend, f.young, true)
	requireExists(t, backend, f.untouched, true)
	require.Contains(t, out, "0 failed")
}

func TestLocalListSkipsTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	l := &storage.LocalBackend{Root: root}
	require.NoError(t, l.Put(context.Background(), "media/a.jpg", strings.NewReader("abc"), 3, ""))
	require.NoError(t, os.WriteFile(filepath.Join(root, "media", ".upload-123"), []byte("x"), 0o600))

	var keys []string
	require.NoError(t, l.List(context.Background(), "media/", func(o storage.ObjectInfo) error {
		keys = append(keys, o.Key)

		return nil
	}))
	require.Equal(t, []string{"media/a.jpg"}, keys)
}

func TestMediaRemoveDeletesOnlyRemoteCachedCopies(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	defer storage.SetDefault(backend)()

	ctx := context.Background()
	old := time.Now().AddDate(0, 0, -30)
	local := seedAccount(t, gdb, "alice", nil)
	remote := seedAccount(t, gdb, "bob", strPtr("remote.example"))

	cached := createAttachment(t, gdb, remote.Id, "2026/09/01/cached.jpg", strPtr("thumb_cached.jpg"), "https://remote.example/a.jpg", old)
	legacy := createAttachment(t, gdb, remote.Id, "refreshed/7_b.jpg", nil, "https://remote.example/b.jpg", old)
	shared := createAttachment(t, gdb, remote.Id, "2026/09/01/shared.jpg", nil, "https://remote.example/c.jpg", old)
	createAttachment(t, gdb, local.Id, "2026/09/01/shared.jpg", nil, "", old)
	recent := createAttachment(t, gdb, remote.Id, "2026/09/30/recent.jpg", nil, "https://remote.example/d.jpg", time.Now())
	imported := createAttachment(t, gdb, local.Id, "2026/09/01/imported.jpg", nil, "https://old.example/e.jpg", old)
	own := createAttachment(t, gdb, local.Id, "2026/09/01/own.jpg", strPtr("thumb_own.jpg"), "", old)

	for _, key := range []string{"media/2026/09/01/cached.jpg", "media/2026/09/01/thumb_cached.jpg", "refreshed/7_b.jpg", "media/2026/09/01/shared.jpg", "media/2026/09/30/recent.jpg", "media/2026/09/01/imported.jpg", "media/2026/09/01/own.jpg", "media/2026/09/01/thumb_own.jpg"} {
		require.NoError(t, backend.Put(ctx, key, strings.NewReader("x"), 1, ""))
	}

	out, err := runMediaCLI(t, "remove", "--days", "7", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "Found 3 cached remote media attachments")
	require.Len(t, backend.Keys(), 8)

	_, err = runMediaCLI(t, "remove", "--days", "7")
	require.NoError(t, err)

	requireExists(t, backend, []string{"media/2026/09/01/cached.jpg", "media/2026/09/01/thumb_cached.jpg", "refreshed/7_b.jpg"}, false)
	requireExists(t, backend, []string{"media/2026/09/01/shared.jpg", "media/2026/09/30/recent.jpg", "media/2026/09/01/imported.jpg", "media/2026/09/01/own.jpg", "media/2026/09/01/thumb_own.jpg"}, true)

	reload := func(id uint64) models.MediaAttachment {
		var m models.MediaAttachment
		require.NoError(t, gdb.First(&m, id).Error)

		return m
	}
	for _, id := range []uint64{cached.Id, legacy.Id, shared.Id} {
		m := reload(id)
		require.Empty(t, m.FilePath, "row %d falls back to remote_url", id)
		require.Nil(t, m.ThumbnailPath)
		require.NotEmpty(t, m.RemoteUrl)
	}
	require.Equal(t, "2026/09/30/recent.jpg", reload(recent.Id).FilePath)
	require.Equal(t, "2026/09/01/imported.jpg", reload(imported.Id).FilePath)
	require.Equal(t, "2026/09/01/own.jpg", reload(own.Id).FilePath)
}

func TestPruneProfilesKeepsHotlinkedURLs(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	defer storage.SetDefault(backend)()

	remote := seedAccount(t, gdb, "bob", strPtr("remote.example"))
	require.NoError(t, gdb.Model(remote).Updates(map[string]any{"avatar": "https://remote.example/a.png", "header": "avatars/bob_header.png"}).Error)
	require.NoError(t, backend.Put(context.Background(), "avatars/bob_header.png", strings.NewReader("x"), 1, ""))

	_, err := runMediaCLI(t, "remove", "--prune-profiles", "--include-follows")
	require.NoError(t, err)

	var got models.Account
	require.NoError(t, gdb.First(&got, remote.Id).Error)
	require.Equal(t, "https://remote.example/a.png", got.Avatar)
	require.Empty(t, got.Header)
	require.Empty(t, backend.Keys())
}

func TestMediaUsageSumsObjectSizes(t *testing.T) {
	setupMediaDB(t)
	backend := storagetest.NewMemory()
	defer storage.SetDefault(backend)()

	ctx := context.Background()
	require.NoError(t, backend.Put(ctx, "media/a", strings.NewReader("12345"), 5, ""))
	require.NoError(t, backend.Put(ctx, "media/b/c", strings.NewReader("123"), 3, ""))
	require.NoError(t, backend.Put(ctx, "emojis/e.png", strings.NewReader("1234567"), 7, ""))

	out, err := runMediaCLI(t, "usage")
	require.NoError(t, err)
	require.Regexp(t, `media/\s+2 objects\s+8 B`, out)
	require.Regexp(t, `emojis/\s+1 objects\s+7 B`, out)
	require.Contains(t, out, "(15 bytes)")
	require.Contains(t, out, "Storage (s3 backend)")
}

func TestStoreRefreshedMediaUsesMediaKeys(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	defer storage.SetDefault(backend)()

	ctx := context.Background()
	remote := seedAccount(t, gdb, "bob", strPtr("remote.example"))
	a := createAttachment(t, gdb, remote.Id, "refreshed/1_old.png", nil, "https://remote.example/files/pic.PNG?x=1", time.Now())
	require.NoError(t, backend.Put(ctx, "refreshed/1_old.png", strings.NewReader("old"), 3, ""))

	require.NoError(t, storeRefreshedMedia(ctx, a, []byte("new-bytes")))

	var got models.MediaAttachment
	require.NoError(t, gdb.First(&got, a.Id).Error)
	require.NotContains(t, got.FilePath, "refreshed/")
	require.True(t, strings.HasSuffix(got.FilePath, ".png"), got.FilePath)
	require.Equal(t, int64(9), got.FileSize)
	require.Equal(t, []string{got.StorageKey()}, backend.Keys(), "new object under media/, old copy removed")
	require.True(t, strings.HasPrefix(got.StorageKey(), "media/"))
}

func pngBytes(t *testing.T, c color.Color) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, c)

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	return buf.Bytes()
}

func TestEmojiImportStoresUnderEmojiKeys(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	defer storage.SetDefault(backend)()

	ctx := context.Background()

	res, err := importEmojiData(ctx, "blob", pngBytes(t, color.White), "cat", false, false)
	require.NoError(t, err)
	require.Equal(t, emojiCreated, res)

	var e models.Emoji
	require.NoError(t, gdb.Where("shortcode = ?", "blob").First(&e).Error)
	require.True(t, strings.HasPrefix(e.Url, testBaseURL+"/emoji-assets/"), e.Url)
	keys := backend.Keys()
	require.Len(t, keys, 2)
	for _, k := range keys {
		require.True(t, strings.HasPrefix(k, "emojis/"), k)
	}

	res, err = importEmojiData(ctx, "blob", pngBytes(t, color.Black), "cat", false, false)
	require.NoError(t, err)
	require.Equal(t, emojiSkipped, res)

	res, err = importEmojiData(ctx, "blob", pngBytes(t, color.Black), "cat", false, true)
	require.NoError(t, err)
	require.Equal(t, emojiUpdated, res)

	var updated models.Emoji
	require.NoError(t, gdb.First(&updated, e.Id).Error)
	require.NotEqual(t, e.Url, updated.Url)
	require.Len(t, backend.Keys(), 2, "replaced asset removed")

	// Export reads the stored object back.
	dir := t.TempDir()
	app := &cli.App{Name: "tootctl", Commands: []*cli.Command{EmojiCommand()}}
	require.NoError(t, app.Run([]string{"tootctl", "emoji", "export", filepath.Join(dir, "out.tar.gz")}))
	st, err := os.Stat(filepath.Join(dir, "out.tar.gz"))
	require.NoError(t, err)
	require.Positive(t, st.Size())
}
