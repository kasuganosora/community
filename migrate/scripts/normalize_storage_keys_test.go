package scripts

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
)

func TestNormalizeStorageKeys_RewritesBucketURLs(t *testing.T) {
	gdb, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	require.NoError(t, err)
	require.NoError(t, gdb.AutoMigrate(&models.MediaAttachment{}))
	require.NoError(t, gdb.Exec("CREATE TABLE matrix_media (id INTEGER PRIMARY KEY, file_path VARCHAR(500))").Error)

	rows := map[uint64]string{
		1: "https://acct.r2.cloudflarestorage.com/community-file/media/2026/10/09/a.jpg",
		2: "2026/10/09/b.jpg",
		3: "https://elsewhere.example/whatever.jpg",
		4: "http://localhost:9000/community/media/2026/10/09/c.png",
	}
	for id, fp := range rows {
		require.NoError(t, gdb.Create(&models.MediaAttachment{Id: id, AccountId: 1, FileName: "f", FilePath: fp, ContentType: "image/jpeg"}).Error)
	}
	require.NoError(t, gdb.Exec("INSERT INTO matrix_media (id, file_path) VALUES (1, 'https://acct.r2.cloudflarestorage.com/community-file/matrix/2026/10/09/m.png'), (2, 'uploads/matrix/2026/10/09/n.png')").Error)

	require.NoError(t, NormalizeStorageKeysScript{}.Up(gdb))
	require.NoError(t, NormalizeStorageKeysScript{}.Up(gdb), "idempotent")

	want := map[uint64]string{1: "2026/10/09/a.jpg", 2: "2026/10/09/b.jpg", 3: "https://elsewhere.example/whatever.jpg", 4: "2026/10/09/c.png"}
	for id, fp := range want {
		var m models.MediaAttachment
		require.NoError(t, gdb.First(&m, id).Error)
		assert.Equal(t, fp, m.FilePath, id)
	}

	var paths []string
	require.NoError(t, gdb.Raw("SELECT file_path FROM matrix_media ORDER BY id").Scan(&paths).Error)
	assert.Equal(t, []string{"matrix/2026/10/09/m.png", "uploads/matrix/2026/10/09/n.png"}, paths)
}
