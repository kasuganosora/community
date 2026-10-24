package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openProbeDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := openDatabase(DatabaseParams{Type: "sqlite", Path: filepath.Join(t.TempDir(), "source.db")})
	require.NoError(t, err)
	t.Cleanup(func() { closeDB(conn) })
	return conn
}

func TestInspectMisskeyIsLight(t *testing.T) {
	conn := openProbeDB(t)
	require.NoError(t, conn.Exec(`
		CREATE TABLE "user" (id varchar(32) PRIMARY KEY, host varchar(128));
		CREATE TABLE user_profile ("userId" varchar(32) PRIMARY KEY);
		CREATE TABLE note (id varchar(32) PRIMARY KEY);
		CREATE TABLE drive_file (id varchar(32) PRIMARY KEY, "userHost" varchar(128), url varchar(512), "storedInternal" boolean);
		CREATE TABLE emoji (id varchar(32) PRIMARY KEY, host varchar(128), "originalUrl" varchar(512));
		CREATE TABLE following (id varchar(32) PRIMARY KEY);
	`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO "user" (id, host) VALUES ('9xk2a1b2c3', NULL), ('9xk2a1b2c4', NULL), ('9xk2a1b2c5', 'remote.example')`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO note (id) VALUES ('n1'), ('n2'), ('n3'), ('n4')`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO drive_file (id, "userHost", url, "storedInternal") VALUES
		('f1', NULL, 'http://localhost:18082/files/a.png', 1),
		('f2', NULL, 'http://localhost:18082/files/b.png', 1),
		('f3', 'remote.example', 'https://cdn.example/files/c.png', 0),
		('f4', NULL, 'https://cdn.example/objects/d.png', 0),
		('f5', NULL, '', 1)`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO emoji (id, host, "originalUrl") VALUES ('e1', NULL, 'http://localhost:18082/files/blob.png')`).Error)

	info, err := inspectMisskey(conn)
	require.Nil(t, err)
	require.Equal(t, "aid", info.IDMethod)
	require.Equal(t, "http://localhost:18082", info.DetectedBaseURL)
	require.Equal(t, []string{"http://localhost:18082"}, info.BaseURLCandidates)
	// Catalog estimates are unavailable outside PostgreSQL. A full-table
	// count would have filled these from the rows inserted above.
	require.NotContains(t, info.Counts, "files")
	require.NotContains(t, info.Counts, "notes")
	require.NotContains(t, info.Counts, "local_users")
	require.NotContains(t, info.Counts, "remote_users")
}

func TestInspectMastodonIsLight(t *testing.T) {
	conn := openProbeDB(t)
	require.NoError(t, conn.Exec(`
		CREATE TABLE accounts (id integer PRIMARY KEY, domain varchar(128));
		CREATE TABLE users (id integer PRIMARY KEY);
		CREATE TABLE statuses (id integer PRIMARY KEY, account_id integer, uri varchar(512));
		CREATE TABLE media_attachments (id integer PRIMARY KEY);
		CREATE TABLE custom_emojis (id integer PRIMARY KEY, domain varchar(128));
		CREATE TABLE follows (id integer PRIMARY KEY);
	`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO accounts (id, domain) VALUES (1, NULL), (2, NULL), (3, 'remote.example')`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO users (id) VALUES (1), (2)`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO statuses (id, account_id, uri) VALUES
		(1, 1, 'https://old.example/users/alice/statuses/1'),
		(2, 1, 'https://old.example/users/alice/statuses/2'),
		(3, 3, 'https://remote.example/users/carol/statuses/9')`).Error)
	require.NoError(t, conn.Exec(`INSERT INTO media_attachments (id) VALUES (1), (2), (3), (4)`).Error)

	info, err := inspectMastodon(conn)
	require.Nil(t, err)
	require.Equal(t, "https://old.example", info.DetectedBaseURL)
	require.NotContains(t, info.Counts, "files")
	require.NotContains(t, info.Counts, "notes")
	require.NotContains(t, info.Counts, "local_users")
	require.NotContains(t, info.Counts, "remote_users")
}

func TestInspectFunctionsDoNotCount(t *testing.T) {
	for _, fn := range []struct{ file, name string }{
		{"misskey.go", "inspectMisskey"},
		{"mastodon.go", "inspectMastodon"},
		{"server.go", "prepareInstall"},
		{"server.go", "testMisskey"},
		{"server.go", "testMastodon"},
	} {
		body := functionSource(t, fn.file, fn.name)
		require.NotContains(t, strings.ToLower(body), "count(*)", fn.name)
	}
	// The connect handlers still call the light inspectors. The install
	// request must not open or inspect the source itself.
	require.Contains(t, functionSource(t, "server.go", "testMisskey"), "inspectMisskey(")
	require.Contains(t, functionSource(t, "server.go", "testMastodon"), "inspectMastodon(")
	prepare := functionSource(t, "server.go", "prepareInstall")
	require.NotContains(t, prepare, "inspectMisskey(")
	require.NotContains(t, prepare, "inspectMastodon(")
	require.NotContains(t, prepare, "openMisskey(")
	require.NotContains(t, prepare, "openMastodon(")
}

func functionSource(t *testing.T, file, name string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	require.NoError(t, err)
	src := string(data)
	needle := name + "("
	start := -1
	for from := 0; from < len(src); {
		i := strings.Index(src[from:], needle)
		if i < 0 {
			break
		}
		abs := from + i
		line := strings.TrimSpace(src[strings.LastIndex(src[:abs], "\n")+1 : abs])
		if line == "func" || strings.HasPrefix(line, "func ") {
			start = strings.LastIndex(src[:abs], "\n") + 1
			break
		}
		from = abs + len(needle)
	}
	require.NotEqual(t, -1, start, name)
	rest := src[start+1:]
	next := strings.Index(rest, "\nfunc ")
	if next < 0 {
		return src[start:]
	}
	return src[start : start+1+next]
}

func TestSplitNullFraction(t *testing.T) {
	frac := 0.25
	local, remote := splitNullFraction(1000, &frac)
	require.Equal(t, int64(250), local)
	require.Equal(t, int64(750), remote)
	local, remote = splitNullFraction(1000, nil)
	require.Equal(t, int64(1000), local)
	require.Equal(t, int64(0), remote)
	local, remote = splitNullFraction(0, &frac)
	require.Equal(t, int64(0), local)
	require.Equal(t, int64(0), remote)
}

func TestMigrationJobsProbeBeforeConfig(t *testing.T) {
	misskey := stageNames("misskey")
	require.Equal(t, []string{"source_probe", "config"}, misskey[:2])
	mastodon := stageNames("mastodon")
	require.Equal(t, []string{"source_probe", "config"}, mastodon[:2])
	require.NotContains(t, stageNames("fresh"), "source_probe")
}

func TestPrepareInstallDoesNotDialSource(t *testing.T) {
	withConfigDir(t)
	mr := miniredis.RunT(t)
	dir := t.TempDir()
	files := filepath.Join(dir, "files")
	require.NoError(t, os.MkdirAll(files, 0o700))
	base := InstallRequest{
		Locale:   "en",
		Domain:   "https://old.example",
		Database: DatabaseParams{Type: "sqlite", Path: filepath.Join(dir, "app.db")},
		Redis:    RedisParams{Address: mr.Addr()},
		Admin:    AdminParams{Username: "admin", Email: "ops@example.org", Password: "longpassword"},
	}
	misskey := base
	misskey.Mode = "misskey"
	misskey.Misskey = &MisskeyParams{
		Host: "127.0.0.1", Port: 1, Name: "misskey", User: "misskey", Password: "source-secret",
		SSLMode: "disable", IDMethod: "aid", MediaSource: "s3", MediaMode: "copy",
		ObjectStorage: ObjectStorageParams{Endpoint: "http://127.0.0.1:1", Bucket: "media", AccessKey: "key", SecretKey: "sek", PublicURL: "https://cdn.example"},
	}
	job, secrets, err := prepareInstall(context.Background(), &misskey)
	require.Nil(t, err)
	require.Equal(t, "https://old.example", job.BaseURL)
	require.Equal(t, "aid", job.Misskey.IDMethod)
	require.Empty(t, job.Misskey.Password)
	require.Empty(t, job.Misskey.ObjectStorage.SecretKey)
	require.Equal(t, "source-secret", secrets.MisskeyPassword)
	require.Equal(t, "sek", secrets.S3SecretKey)
	require.Equal(t, "source_probe", job.Stages[0].Name)

	mastodon := base
	mastodon.Database.Path = filepath.Join(dir, "app-mastodon.db")
	mastodon.Mode = "mastodon"
	mastodon.Mastodon = &MastodonParams{
		Host: "127.0.0.1", Port: 1, Name: "mastodon", User: "mastodon", Password: "masto-secret",
		SSLMode: "disable", MediaSource: "local", FilesPath: files,
	}
	job, secrets, err = prepareInstall(context.Background(), &mastodon)
	require.Nil(t, err)
	require.Equal(t, "https://old.example", job.BaseURL)
	require.Empty(t, job.Mastodon.Password)
	require.Equal(t, "masto-secret", secrets.MisskeyPassword)
	require.Equal(t, "source_probe", job.Stages[0].Name)
}
