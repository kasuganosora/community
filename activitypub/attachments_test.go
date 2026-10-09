package activitypub

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
	"net/http/httptest"
	"testing"
)

func attachmentFixture(rawURL, kind string) map[string]any {
	return map[string]any{"type": "Document", "url": rawURL, "mediaType": kind, "name": "<b>Alternative text</b>", "width": 96, "height": 64}
}
func TestIncomingAttachmentsMetadataAndSafety(t *testing.T) {
	ctx := context.Background()
	inputs := []any{attachmentFixture("https://8.8.8.8/picture.png", "image/png"), attachmentFixture("http://127.0.0.1/private", "image/png"), attachmentFixture("https://8.8.8.8/active.svg", "image/svg+xml"), attachmentFixture("https://user:secret@8.8.8.8/file", "image/png"), attachmentFixture("https://8.8.8.8/file#fragment", "image/png"), attachmentFixture("javascript:alert(1)", "image/png"), attachmentFixture("https://8.8.8.8/notes.txt", "text/plain")}
	got := mustIncomingAttachments(t, ctx, inputs, 99)
	require.Len(t, got, 2)
	require.Equal(t, "Alternative text", got[0].Description)
	require.Equal(t, "picture.png", got[0].FileName)
	require.Equal(t, uint64(99), got[0].AccountId)
	require.Equal(t, 96, *got[0].Width)
	require.Empty(t, got[0].FilePath)
	serialized, err := serializer.ToMediaAttachment(got[0])
	require.NoError(t, err)
	// Browsers load remote files through the media proxy; remote_url is the origin.
	require.Equal(t, mediaproxy.URL(mediaproxy.KindMedia, got[0].RemoteUrl), serialized.Url)
	require.Equal(t, mediaproxy.URL(mediaproxy.KindPreview, got[0].RemoteUrl), serialized.PreviewUrl)
	require.Equal(t, got[0].RemoteUrl, *serialized.RemoteUrl)
	require.Equal(t, models.MediaTypeUnknown, got[1].Type)
	many := make([]any, 30)
	for i := range many {
		many[i] = inputs[0]
	}
	require.Len(t, mustIncomingAttachments(t, ctx, many, 99), 16)
	require.Empty(t, mustIncomingAttachments(t, ctx, attachmentFixture("https://[::1]/private", "image/png"), 99))
}
func TestIncomingAttachmentsPersistAndRollbackWithStatus(t *testing.T) {
	conn := emojiDB(t)
	stmt := &gorm.Statement{DB: conn}
	require.NoError(t, stmt.Parse(&models.MediaAttachment{}))
	for _, f := range stmt.Schema.Fields {
		if f.AutoIncrement {
			f.DataType = schema.Int
		}
	}
	require.NoError(t, conn.AutoMigrate(&models.MediaAttachment{}))
	actor, err := createRemoteAccount(conn, &Actor{ID: "https://remote.example/users/files", PreferredUsername: "files"})
	require.NoError(t, err)
	domain := "remote.example"
	makeStatus := func() models.Status {
		return models.Status{AccountId: actor.Id, Domain: &domain, Content: models.StatusContent{Content: "File"}, Attachments: mustIncomingAttachments(t, context.Background(), attachmentFixture("https://8.8.8.8/file.png", "image/png"), actor.Id)}
	}
	status := makeStatus()
	require.NoError(t, persistIncomingStatusWithEmoji(context.Background(), conn, &status, nil))
	var saved models.Status
	require.NoError(t, conn.Preload("Attachments").First(&saved, status.Id).Error)
	require.Len(t, saved.Attachments, 1)
	require.Equal(t, status.Id, *saved.Attachments[0].StatusId)
	failed := makeStatus()
	failed.Attachments[0].AccountId = 999999
	require.Error(t, persistIncomingStatusWithEmoji(context.Background(), conn, &failed, nil))
	var count int64
	require.NoError(t, conn.Model(&models.Status{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, conn.Model(&models.MediaAttachment{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestIncomingAttachmentUpdateRequiresOwnerAndReplacesAssociation(t *testing.T) {
	conn := emojiDB(t)
	stmt := &gorm.Statement{DB: conn}
	require.NoError(t, stmt.Parse(&models.MediaAttachment{}))
	for _, f := range stmt.Schema.Fields {
		if f.AutoIncrement {
			f.DataType = schema.Int
		}
	}
	require.NoError(t, conn.AutoMigrate(&models.MediaAttachment{}))
	actorURI := "https://remote.example/users/files"
	actor, err := createRemoteAccount(conn, &Actor{ID: actorURI, PreferredUsername: "files"})
	require.NoError(t, err)
	domain := "remote.example"
	uri := "https://remote.example/notes/files"
	status := models.Status{AccountId: actor.Id, Domain: &domain, OriginUri: &uri, Content: models.StatusContent{Content: "Old"}, Attachments: mustIncomingAttachments(t, context.Background(), attachmentFixture("https://8.8.8.8/old.png", "image/png"), actor.Id)}
	require.NoError(t, persistIncomingStatusWithEmoji(context.Background(), conn, &status, nil))
	update := func(actor string, attachments any) int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		ctx := context.WithValue(context.Background(), actorKey, actor)
		handleUpdateActivity(ctx, c, map[string]any{"object": map[string]any{"id": uri, "type": "Note", "content": "Updated", "attachment": attachments}}, nil)
		return w.Code
	}
	require.Equal(t, 403, update("https://evil.example/users/files", []any{}))
	// A canceled DNS-validation budget must not turn an update into a permanent
	// successful removal of its previous attachments.
	failedCtx, cancel := context.WithCancel(context.WithValue(context.Background(), actorKey, actorURI))
	cancel()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleUpdateActivity(context.WithValue(failedCtx, inboxTransactionKey{}, conn), c, map[string]any{"object": map[string]any{"id": uri, "type": "Note", "content": "Must not commit", "attachment": []any{attachmentFixture("https://8.8.8.8/new.png", "image/png")}}}, nil)
	require.Equal(t, 503, w.Code)
	var retained models.StatusContent
	require.NoError(t, conn.Where("status_id = ?", status.Id).First(&retained).Error)
	require.Equal(t, "Old", retained.Content)

	var got []models.MediaAttachment
	require.NoError(t, conn.Where("status_id = ?", status.Id).Find(&got).Error)
	require.Len(t, got, 1)
	require.Equal(t, 200, update(actorURI, []any{attachmentFixture("https://8.8.8.8/new.gif", "image/gif")}))
	got = nil
	require.NoError(t, conn.Where("status_id = ?", status.Id).Find(&got).Error)
	require.Len(t, got, 1)
	require.Equal(t, "https://8.8.8.8/new.gif", got[0].RemoteUrl)
	require.Equal(t, 200, update(actorURI, []any{}))
	got = nil
	require.NoError(t, conn.Where("status_id = ?", status.Id).Find(&got).Error)
	require.Empty(t, got)
}

func mustIncomingAttachments(t *testing.T, ctx context.Context, value any, accountID uint64) models.Attachments {
	t.Helper()
	got, err := incomingAttachments(ctx, value, accountID)
	require.NoError(t, err)
	return got
}
func TestIncomingAttachmentCanceledValidationIsRetryable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := incomingAttachments(ctx, attachmentFixture("https://8.8.8.8/image.png", "image/png"), 1)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, got)
}
