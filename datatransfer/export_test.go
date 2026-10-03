package datatransfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"image"
	"image/png"
	"os"
	"testing"
	"time"
)

func TestExportPersonalOwnershipDirectAudienceAndNoCredentials(t *testing.T) {
	db := importerDB(t)
	ownerUser := models.User{Id: 31, UUID: "export-owner", UserName: "export-owner", EmailHash: "export-hash"}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&ownerUser).Error)
	ownerAccount := models.Account{Id: 41, UserId: 31, Username: "export-owner", Acct: "export-owner@__LOCAL__"}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&ownerAccount).Error)
	opts := Options{OwnerAccountID: 41, OwnerUserID: 31}
	require.NoError(t, db.AutoMigrate(&models.Filter{}, &models.MuteStatus{}, &models.Notification{}, &models.StatusHistory{}))
	clean := db.Session(&gorm.Session{SkipHooks: true})
	peer := models.Account{Id: 42, Username: "peer", Acct: "peer@__LOCAL__"}
	outsider := models.Account{Id: 43, Username: "outsider", Acct: "outsider@__LOCAL__"}
	require.NoError(t, clean.Create(&peer).Error)
	require.NoError(t, clean.Create(&outsider).Error)
	now := time.Now()
	nextID := uint64(100)
	makePost := func(author uint64, body string, recipient uint64) uint64 {
		nextID++
		s := models.Status{Id: nextID, AccountId: author, Visibility: models.StatusVisibilityDirect, CreatedAt: &now, UpdatedAt: &now}
		require.NoError(t, clean.Create(&s).Error)
		require.NoError(t, clean.Create(&models.StatusContent{StatusId: s.Id, Content: body}).Error)
		if recipient > 0 {
			require.NoError(t, clean.Create(&models.StatusToAccount{StatusId: s.Id, AccountId: recipient}).Error)
		}
		return s.Id
	}
	makePost(opts.OwnerAccountID, "OWN_SECRET", peer.Id)
	makePost(peer.Id, "RECEIVED_SECRET", opts.OwnerAccountID)
	makePost(peer.Id, "OUTSIDER_SECRET", outsider.Id)
	t.Chdir(t.TempDir())
	require.NoError(t, os.MkdirAll("uploads/media", 0700))
	payload := []byte("personal media contents")
	require.NoError(t, os.WriteFile("uploads/media/fixture.bin", payload, 0600))
	require.NoError(t, clean.Create(&models.MediaAttachment{Id: 501, AccountId: opts.OwnerAccountID, FileName: "original.bin", FilePath: "fixture.bin", ContentType: "application/octet-stream", FileSize: int64(len(payload))}).Error)
	for _, scope := range []string{"personal", "site", ""} {
		record := StoredPreservedRecord{Namespace: "scope-test", Scope: scope, OwnerUserID: 31, OwnerAccountID: 41, Kind: "post", SourceID: "archived", Data: raw(map[string]string{"text": "scope_secret_" + scope + "_end"})}
		require.NoError(t, db.Create(&record).Error)
	}
	var buf bytes.Buffer
	require.NoError(t, ExportPersonal(context.Background(), db, opts.OwnerAccountID, "https://community.test", &buf))
	a, e := OpenArchive(context.Background(), "backup.zip", bytes.NewReader(buf.Bytes()), Limits{})
	require.NoError(t, e)
	defer a.Close()
	b, e := ParseArchive(context.Background(), a, ParseOptions{})
	require.NoError(t, e)
	require.Len(t, b.Media, 1)
	require.Equal(t, "original.bin", b.Media[0].Name)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(payload)), b.Media[0].SHA256)
	restored, err := a.Read(b.Media[0].Path, 100)
	require.NoError(t, err)
	require.Equal(t, payload, restored)
	require.Len(t, b.Posts, 2)
	require.Equal(t, "OWN_SECRET", b.Posts[0].Text)
	require.False(t, b.Posts[0].ArchiveOnly)
	require.Equal(t, "RECEIVED_SECRET", b.Posts[1].Text)
	require.True(t, b.Posts[1].ArchiveOnly)
	require.NotContains(t, string(raw(b)), "OUTSIDER_SECRET")
	require.Contains(t, string(raw(b)), "scope_secret_personal_end")
	require.NotContains(t, string(raw(b)), "scope_secret_site_end")
	require.NotContains(t, string(raw(b)), "scope_secret__end")
	require.Empty(t, b.Keys)
	require.Empty(t, b.Credentials)
	require.False(t, b.Accounts[0].Admin)
	require.NoError(t, os.Remove("uploads/media/fixture.bin"))
	var incomplete bytes.Buffer
	require.ErrorContains(t, ExportPersonal(context.Background(), db, opts.OwnerAccountID, "https://community.test", &incomplete), "stored media file is unavailable")
	require.NoError(t, clean.Model(&models.MediaAttachment{}).Where("id = ?", 501).Updates(map[string]any{"file_path": "", "remote_url": "https://remote.example/original.bin"}).Error)
	var references bytes.Buffer
	require.NoError(t, ExportPersonal(context.Background(), db, opts.OwnerAccountID, "https://community.test", &references))

}
func TestExportMediaRootRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../secret", "/etc/passwd", "a/../secret", "a\\secret"} {
		_, e := openExportMedia(name)
		require.Error(t, e)
	}
}
func TestExportCompressedLimitDoesNotWritePartialOverLimit(t *testing.T) {
	var b bytes.Buffer
	w := &limitedBackupWriter{w: &b, remaining: 3}
	n, e := w.Write([]byte("four"))
	require.Error(t, e)
	require.Zero(t, n)
	require.Empty(t, b.Bytes())
}

func TestExportNativeImportedMetadataAndProfileBytesRoundTrip(t *testing.T) {
	db := importerDB(t)
	require.NoError(t, db.AutoMigrate(&models.Filter{}, &models.MuteStatus{}, &models.Notification{}, &models.StatusHistory{}))
	t.Chdir(t.TempDir())
	require.NoError(t, os.MkdirAll("uploads/media", 0700))
	require.NoError(t, os.MkdirAll("uploads/public/avatars", 0700))
	var img bytes.Buffer
	require.NoError(t, png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	require.NoError(t, os.WriteFile("uploads/media/picture.png", img.Bytes(), 0600))
	require.NoError(t, os.WriteFile("uploads/public/avatars/owner", img.Bytes(), 0600))
	clean := db.Session(&gorm.Session{SkipHooks: true})
	for _, u := range []models.User{{Id: 31, UUID: "source", UserName: "source", EmailHash: "source"}, {Id: 32, UUID: "target", UserName: "target", EmailHash: "target"}} {
		require.NoError(t, clean.Create(&u).Error)
	}
	owner := models.Account{Id: 41, UserId: 31, Username: "source", Acct: "source@__LOCAL__", Note: "rich text", ImportedNoteHTML: "<p><strong>rich</strong> text</p>", LocalActorURI: "https://community.test/users/original-actor", Avatar: "public/avatars/owner", Header: "https://community.test/media/51/picture.png"}
	target := models.Account{Id: 42, UserId: 32, Username: "target", Acct: "target@__LOCAL__"}
	require.NoError(t, clean.Create(&owner).Error)
	require.NoError(t, clean.Create(&target).Error)
	require.NoError(t, clean.Create(&models.AccountExtra{AccountId: 41}).Error)
	require.NoError(t, clean.Create(&models.AccountExtra{AccountId: 42}).Error)
	emoji := models.Emoji{Id: 61, Shortcode: "wave", Domain: "remote.example", Url: "https://remote.example/wave.png", StaticUrl: "https://remote.example/wave.png"}
	require.NoError(t, clean.Create(&emoji).Error)
	require.NoError(t, db.Exec("INSERT INTO account_emojis(account_id,emoji_id) VALUES (?,?)", 41, 61).Error)
	require.NoError(t, clean.Create(&models.MediaAttachment{Id: 51, AccountId: 41, FileName: "picture.png", FilePath: "picture.png", ContentType: "image/png", FileSize: int64(img.Len())}).Error)
	now := time.Now()
	require.NoError(t, clean.Create(&models.Status{Id: 71, AccountId: 41, Visibility: models.StatusVisibilityPublic, LocalOnly: true, LocalObjectURI: "https://community.test/users/original-actor/statuses/old-id", Properties: map[string]any{"import_original_html": "<p><em>original rich post</em></p>"}, CreatedAt: &now, UpdatedAt: &now}).Error)
	require.NoError(t, clean.Create(&models.StatusContent{StatusId: 71, Content: "original rich post"}).Error)

	archived := StoredPreservedRecord{Namespace: "old-archive-ns", Scope: "personal", OwnerUserID: 31, OwnerAccountID: 41, Kind: "post", SourceID: "received", Data: raw(Post{ID: "received", AuthorID: "remote-author", Text: "archived picture", Visibility: "direct", MediaIDs: []string{"archive-media", "51"}})}
	require.NoError(t, db.Create(&archived).Error)
	require.NoError(t, db.Create(&ImportReceipt{Namespace: "old-archive-ns", Kind: "media", SourceID: "archive-media", SourceHash: "archive-media-hash", Digest: "digest", TargetID: 51}).Error)
	var buf bytes.Buffer
	require.NoError(t, ExportPersonal(context.Background(), db, 41, "https://community.test", &buf))
	a, e := OpenArchive(context.Background(), "backup.zip", &buf, Limits{})
	require.NoError(t, e)
	defer a.Close()
	bundle, e := ParseArchive(context.Background(), a, ParseOptions{})
	require.NoError(t, e)
	require.Equal(t, owner.LocalActorURI, bundle.Accounts[0].ActorURI)
	require.True(t, bundle.Accounts[0].NoteHTML)
	require.Equal(t, owner.ImportedNoteHTML, bundle.Accounts[0].Note)
	require.Equal(t, []string{"61"}, bundle.Accounts[0].EmojiIDs)
	require.Len(t, bundle.Emojis, 1)
	require.True(t, bundle.Posts[0].LocalOnly)
	require.Equal(t, "https://community.test/users/original-actor/statuses/old-id", bundle.Posts[0].URI)
	require.Equal(t, "<p><em>original rich post</em></p>", bundle.Posts[0].HTML)
	for _, id := range []string{bundle.Accounts[0].AvatarID, bundle.Accounts[0].HeaderID} {
		require.NotEmpty(t, id)
		found := false
		for _, m := range bundle.Media {
			if m.ID == id {
				found = true
				require.Equal(t, "image/png", m.ContentType)
				require.Empty(t, m.RemoteURL)
				bytes, e := a.Read(m.Path, 1024)
				require.NoError(t, e)
				require.Equal(t, img.Bytes(), bytes)
			}
		}
		require.True(t, found)
	}
	report, e := Import(context.Background(), db, bundle, Options{Scope: "personal", OwnerUserID: 32, OwnerAccountID: 42, TargetBaseURL: "https://community.test", ArchiveDir: a.Root(), Files: a.Files, StorageDir: "uploads/media"}, nil)
	require.NoError(t, e, "%+v", report)
	var restored models.Account
	require.NoError(t, db.Preload("Emojis").First(&restored, 42).Error)
	require.Equal(t, owner.ImportedNoteHTML, restored.ImportedNoteHTML)
	require.Contains(t, restored.Avatar, "https://community.test/media/")
	require.Contains(t, restored.Header, "https://community.test/media/")
	require.Len(t, restored.Emojis, 1)
	var post models.Status
	require.NoError(t, db.Where("account_id = ?", 42).First(&post).Error)
	require.True(t, post.LocalOnly)
	require.Equal(t, "<p><em>original rich post</em></p>", post.Properties["import_original_html"])
	var received StoredPreservedRecord
	require.NoError(t, db.Where("owner_account_id = ? AND kind = ?", 42, "post").First(&received).Error)
	var archivedPost Post
	require.NoError(t, json.Unmarshal(received.Data, &archivedPost))
	require.Equal(t, "archived picture", archivedPost.Text)
	require.Equal(t, []string{"51", "unresolved:51"}, archivedPost.MediaIDs)
	var mapping ImportReceipt
	require.NoError(t, db.Where("namespace = ? AND kind = ? AND source_id = ?", received.Namespace, "media", archivedPost.MediaIDs[0]).First(&mapping).Error)
	var attachment models.MediaAttachment
	require.NoError(t, db.Where("id = ? AND account_id = ?", mapping.TargetID, 42).First(&attachment).Error)
	picture, e := os.ReadFile("uploads/media/" + attachment.FilePath)
	require.NoError(t, e)
	require.Equal(t, img.Bytes(), picture)
	var missing int64
	require.NoError(t, db.Model(&ImportReceipt{}).Where("namespace = ? AND kind = ? AND source_id = ?", received.Namespace, "media", archivedPost.MediaIDs[1]).Count(&missing).Error)
	require.Zero(t, missing)

}
