package datatransfer

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// Local Misskey drive files that no note, avatar or banner references are
// imported as unattached media owned by their original user.
func TestUnattachedDriveFilesImported(t *testing.T) {
	b := streamFixture(t, 3, 4)
	for _, id := range []string{"d1", "d2"} {
		b.Media = append(b.Media, Media{ID: id, OwnerID: "u1", Name: id + ".png", ContentType: "image/png", RemoteURL: "https://files.site.example/drive/" + id + ".png", StorageReference: true, Unattached: true})
	}
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	conn := importerDB(t)
	sp := spoolBundle(t, b)
	report, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 2, Apply: true})
	require.NoError(t, err, "%+v", report)
	var rows []models.MediaAttachment
	require.NoError(t, conn.Where("remote_url LIKE ?", "https://files.site.example/drive/%").Find(&rows).Error)
	require.Len(t, rows, 2)
	var owner models.Account
	require.NoError(t, conn.Where("username = ?", "u1").First(&owner).Error)
	for _, m := range rows {
		require.Nil(t, m.StatusId)
		require.Equal(t, owner.Id, m.AccountId)
	}
	require.Equal(t, 2, report.Created["unattached_media"])
}
