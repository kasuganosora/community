package commands

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPurgeUnattachedCLIIsDryRunByDefault(t *testing.T) {
	gdb := setupMediaDB(t)
	backend := storagetest.NewMemory()
	t.Cleanup(storage.SetDefault(backend))

	owner := seedAccount(t, gdb, "purge", nil)
	old := time.Now().Add(-8 * 24 * time.Hour)
	row := createAttachment(t, gdb, owner.Id, "2026/10/01/old.png", strPtr("thumb_old.png"), "", old)
	require.NoError(t, gdb.Model(row).Where("id = ?", row.Id).UpdateColumns(map[string]any{"updated_at": old, "status_id": nil}).Error)
	ctx := context.Background()
	require.NoError(t, backend.Put(ctx, row.StorageKey(), strings.NewReader("orig"), 4, "image/png"))
	require.NoError(t, backend.Put(ctx, row.ThumbnailStorageKey(), strings.NewReader("thumb"), 5, "image/png"))

	out, err := runMediaCLI(t, "purge-unattached")
	require.NoError(t, err)
	require.Contains(t, out, "DRY RUN")
	require.Contains(t, out, "would remove 1")
	_, err = backend.Stat(ctx, row.StorageKey())
	require.NoError(t, err)

	_, err = runMediaCLI(t, "purge-unattached", "--confirm", "--min-age", "10m")
	require.ErrorContains(t, err, "--min-age")
	_, err = backend.Stat(ctx, row.StorageKey())
	require.NoError(t, err)

	out, err = runMediaCLI(t, "purge-unattached", "--confirm", "--dry-run")
	require.NoError(t, err)
	require.Contains(t, out, "DRY RUN")

	out, err = runMediaCLI(t, "purge-unattached", "--confirm")
	require.NoError(t, err)
	require.Contains(t, out, "DELETING")
	require.Contains(t, out, "removed 1")
	require.ErrorIs(t, gdb.First(&models.MediaAttachment{}, row.Id).Error, gorm.ErrRecordNotFound)
	_, statErr := backend.Stat(ctx, row.StorageKey())
	require.ErrorIs(t, statErr, storage.ErrNotFound)
	_, statErr = backend.Stat(ctx, row.ThumbnailStorageKey())
	require.ErrorIs(t, statErr, storage.ErrNotFound)
}
