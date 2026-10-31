package status

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
)

func createMedia(t *testing.T, ts *testSetup, accountId uint64) *models.MediaAttachment {
	t.Helper()

	thumb := "thumb_photo.png"
	m := &models.MediaAttachment{
		AccountId: accountId, FileName: "photo.png", FilePath: "2026/10/09/photo.png", ThumbnailPath: &thumb,
		ContentType: "image/png", Type: models.MediaTypeImage, Description: "a photo",
	}
	require.NoError(t, ts.db.Create(m).Error)

	return m
}

// Posting with media_ids links the uploaded media_attachments rows to the
// status and returns them in media_attachments.
func TestCreate_WithMedia_LinksMediaAttachments(t *testing.T) {
	ts := newTestSetup(t)
	backend := storagetest.NewMemory()
	t.Cleanup(storage.SetDefault(backend))
	m := createMedia(t, ts, ts.acct.Id)
	ctx := context.Background()
	require.NoError(t, backend.Put(ctx, m.StorageKey(), strings.NewReader("orig"), 4, "image/png"))
	require.NoError(t, backend.Put(ctx, m.ThumbnailStorageKey(), strings.NewReader("thumb"), 5, "image/png"))

	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status:     "with a photo",
		Visibility: "public",
		MediaIds:   []string{strconv.FormatUint(m.Id, 10)},
	})
	setAccount(c, ts.acct)
	safeHandler(c, Create)
	require.Equal(t, 200, w.Code, w.Body.String())

	var resp struct {
		Id               string `json:"id"`
		MediaAttachments []struct {
			Id          string `json:"id"`
			Type        string `json:"type"`
			Url         string `json:"url"`
			Description string `json:"description"`
		} `json:"media_attachments"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.MediaAttachments, 1)
	assert.Equal(t, strconv.FormatUint(m.Id, 10), resp.MediaAttachments[0].Id)
	assert.Equal(t, "image", resp.MediaAttachments[0].Type)
	assert.Contains(t, resp.MediaAttachments[0].Url, "/media/"+strconv.FormatUint(m.Id, 10)+"/photo.png")

	var stored models.MediaAttachment
	require.NoError(t, ts.db.First(&stored, m.Id).Error)
	require.NotNil(t, stored.StatusId)
	assert.Equal(t, resp.Id, strconv.FormatUint(*stored.StatusId, 10))

	// The same media cannot be attached to a second status.
	c2, w2 := newGinContextPost(t, nil, CreateStatusRequest{
		Status: "reuse", Visibility: "public", MediaIds: []string{strconv.FormatUint(m.Id, 10)},
	})
	setAccount(c2, ts.acct)
	safeHandler(c2, Create)
	assert.Equal(t, 422, w2.Code, w2.Body.String())

	// Deleting the status detaches the media instead of failing on the FK.
	c3, w3 := newGinContext(t, gin.Params{{Key: "id", Value: resp.Id}})
	setAccount(c3, ts.acct)
	safeHandler(c3, Delete)
	require.Less(t, w3.Code, 300, w3.Body.String())

	require.NoError(t, ts.db.First(&stored, m.Id).Error)
	assert.Nil(t, stored.StatusId)
	require.NotNil(t, stored.DetachedAt)
	_, err := backend.Stat(ctx, stored.StorageKey())
	require.NoError(t, err)
	_, err = backend.Stat(ctx, stored.ThumbnailStorageKey())
	require.NoError(t, err)

	// Redraft reuses the same attachment. Objects stay, and the row is attached again.
	c4, w4 := newGinContextPost(t, nil, CreateStatusRequest{
		Status: "redraft", Visibility: "public", MediaIds: []string{strconv.FormatUint(m.Id, 10)},
	})
	setAccount(c4, ts.acct)
	safeHandler(c4, Create)
	require.Equal(t, 200, w4.Code, w4.Body.String())
	require.NoError(t, ts.db.First(&stored, m.Id).Error)
	require.NotNil(t, stored.StatusId)
	assert.NotEqual(t, resp.Id, strconv.FormatUint(*stored.StatusId, 10))
	_, err = backend.Stat(ctx, stored.StorageKey())
	require.NoError(t, err)
	_, err = backend.Stat(ctx, stored.ThumbnailStorageKey())
	require.NoError(t, err)
}

func TestCreate_WithSomeoneElsesMedia_Rejected(t *testing.T) {
	ts := newTestSetup(t)
	other := ts.fix.CreateAccount(ts.fix.CreateUser(func(u *models.User) { u.UserName = "mediaowner" }),
		func(a *models.Account) { a.Username = "mediaowner" })
	m := createMedia(t, ts, other.Id)

	c, w := newGinContextPost(t, nil, CreateStatusRequest{
		Status: "steal", Visibility: "public", MediaIds: []string{strconv.FormatUint(m.Id, 10)},
	})
	setAccount(c, ts.acct)
	safeHandler(c, Create)
	assert.Equal(t, 422, w.Code, w.Body.String())
}
