package homeserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/emojiasset"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/storage/storagetest"
	"github.com/stretchr/testify/require"
)

func webGroupMediaTestHS(t *testing.T) (*HomeServer, *gin.Engine, string) {
	t.Helper()
	setupMinimalTestDB(t)
	require.NoError(t, db.GetDB().AutoMigrate(&AsyncMediaInfo{}, &WebGroupUpload{}, &MatrixThumbnail{}, &BridgeConversation{}, &models.MatrixDMDelivery{}))
	hs := fullHS("test.local")
	t.Cleanup(resetGlobalHS(hs))
	t.Cleanup(storage.SetDefault(storagetest.NewMemory()))
	roomID := createRoomWithUser(t, hs, "@alice:test.local")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("web_group_identity", true) // Only the Community login middleware is bypassed.
		c.Set("user_id", c.GetHeader("X-Test-User"))
	})
	r.POST("/api/v1/chat/groups/:id/uploads", handleWebGroupUpload)
	r.GET("/api/v1/chat/groups/:id/messages/:messageID/media/:mediaID", handleWebGroupMedia)
	r.GET("/api/v1/chat/groups/:id/avatar", handleWebGroupAvatar)
	r.GET("/api/v1/chat/groups/:id/members/:memberID/avatar", handleWebGroupMemberAvatar)
	return hs, r, roomID
}

func uploadWebGroupFixture(t *testing.T, router *gin.Engine, roomID, user, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = part.Write(data)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/groups/"+webGroupID(roomID)+"/uploads", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Test-User", user)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func webGroupFixtureUploadID(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	require.Equal(t, 201, response.Code, response.Body.String())
	var result struct{ ID string }
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.NotEmpty(t, result.ID)
	return result.ID
}

func webGroupFixtureDownload(router *gin.Engine, mediaURL, user string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, mediaURL, nil)
	req.Header.Set("X-Test-User", user)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	return response
}

func TestWebGroupMediaUploadScopeAndExpiry(t *testing.T) {
	_, router, roomID := webGroupMediaTestHS(t)
	conn := db.GetDB()
	id := webGroupFixtureUploadID(t, uploadWebGroupFixture(t, router, roomID, "@alice:test.local", "note.txt", []byte("hello file")))
	content, err := webGroupUploadContent(conn, roomID, "@alice:test.local", id)
	require.Nil(t, err)
	require.Equal(t, "m.file", content["msgtype"])
	_, err = webGroupUploadContent(conn, roomID, "@bob:test.local", id)
	require.NotNil(t, err, "another user cannot consume a known upload ID")
	_, err = webGroupUploadContent(conn, "!other:test.local", "@alice:test.local", id)
	require.NotNil(t, err, "an upload cannot be moved to another room")
	require.Error(t, webGroupMarkUploadUsed(conn, &MatrixEvent{EventID: "$wrong", RoomID: roomID, Sender: "@bob:test.local"}, id))
	require.NoError(t, conn.Model(&WebGroupUpload{}).Where("id = ?", id).Update("expires_at", time.Now().Add(-time.Minute)).Error)
	_, err = webGroupUploadContent(conn, roomID, "@alice:test.local", id)
	require.NotNil(t, err, "expired upload cannot be sent")
}

func TestWebGroupMediaEventAuthorizationAndByteIntegrity(t *testing.T) {
	hs, router, roomID := webGroupMediaTestHS(t)
	empty := ""
	_, e := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.history_visibility", nil, map[string]any{"history_visibility": "joined"}, &empty)
	require.Nil(t, e)
	payload := []byte("private file: 中文\n\x00binary bytes\xff")
	id := webGroupFixtureUploadID(t, uploadWebGroupFixture(t, router, roomID, "@alice:test.local", "report.bin", payload))
	content, e := webGroupUploadContent(db.GetDB(), roomID, "@alice:test.local", id)
	require.Nil(t, e)
	sent, e := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.message", nil, content, nil)
	require.Nil(t, e)
	var event MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", sent.EventID).First(&event).Error)
	require.NoError(t, webGroupMarkUploadUsed(db.GetDB(), &event, id))
	attachments := webGroupAttachments(roomID, event)
	require.Len(t, attachments, 1)
	url := attachments[0].URL
	response := webGroupFixtureDownload(router, url, "@alice:test.local")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, payload, response.Body.Bytes())
	require.Equal(t, "private, no-store", response.Header().Get("Cache-Control"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Contains(t, response.Header().Get("Content-Disposition"), "attachment")
	response = webGroupFixtureDownload(router, url, "@mallory:test.local")
	require.Equal(t, 403, response.Code)
	joinUser(t, hs, roomID, "@bob:test.local")
	response = webGroupFixtureDownload(router, url, "@bob:test.local")
	require.Equal(t, 404, response.Code, "later joining does not reveal joined-only history")
	forged := strings.Replace(url, attachments[0].ID, base64.RawURLEncoding.EncodeToString([]byte("mxc://test.local/someone-elses-media")), 1)
	require.Equal(t, 404, webGroupFixtureDownload(router, forged, "@alice:test.local").Code)
	_, e = hs.Event.RedactEvent(roomID, event.EventID, "@alice:test.local", "remove", nil)
	require.Nil(t, e)
	require.Equal(t, 404, webGroupFixtureDownload(router, url, "@alice:test.local").Code, "redacted attachments are no longer available through the web event endpoint")
}

func TestWebGroupMediaBoundsAndEncryption(t *testing.T) {
	hs, router, roomID := webGroupMediaTestHS(t)
	t.Setenv("MATRIX_WEB_GROUP_UPLOAD_MAX_BYTES", "128")
	response := uploadWebGroupFixture(t, router, roomID, "@alice:test.local", "large.bin", bytes.Repeat([]byte{1}, 129))
	require.Equal(t, 413, response.Code)
	var count int64
	require.NoError(t, db.GetDB().Model(&MatrixMedia{}).Count(&count).Error)
	require.Zero(t, count, "oversized files are never committed to storage")
	empty := ""
	_, e := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.encryption", nil, map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}, &empty)
	require.Nil(t, e)
	response = uploadWebGroupFixture(t, router, roomID, "@alice:test.local", "small.txt", []byte("small"))
	require.Equal(t, 409, response.Code)
	require.Contains(t, response.Body.String(), "encrypted")
}

func TestWebGroupMediaRejectsMultipleParts(t *testing.T) {
	_, router, roomID := webGroupMediaTestHS(t)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, name := range []string{"one.txt", "two.txt"} {
		part, err := w.CreateFormFile("file", name)
		require.NoError(t, err)
		_, err = part.Write([]byte("some bytes"))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/groups/"+webGroupID(roomID)+"/uploads", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("X-Test-User", "@alice:test.local")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	require.Equal(t, 400, response.Code)
	var count int64
	require.NoError(t, db.GetDB().Model(&MatrixMedia{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestWebGroupMediaCurrentAvatarAndProtectedMemberAvatar(t *testing.T) {
	hs, router, roomID := webGroupMediaTestHS(t)
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	id := webGroupFixtureUploadID(t, uploadWebGroupFixture(t, router, roomID, "@alice:test.local", "photo.png", pngData.Bytes()))
	content, e := webGroupUploadContent(db.GetDB(), roomID, "@alice:test.local", id)
	require.Nil(t, e)
	empty := ""
	_, e = hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.history_visibility", nil, map[string]any{"history_visibility": "joined"}, &empty)
	require.Nil(t, e)
	_, e = hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.avatar", nil, map[string]any{"url": content["url"], "info": content["info"]}, &empty)
	require.Nil(t, e)
	joinUser(t, hs, roomID, "@bob:test.local")
	roomAvatar := "/api/v1/chat/groups/" + webGroupID(roomID) + "/avatar"
	response := webGroupFixtureDownload(router, roomAvatar, "@bob:test.local")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.Equal(t, "image/png", response.Header().Get("Content-Type"))
	require.Contains(t, response.Header().Get("Content-Disposition"), "inline")
	require.Equal(t, 403, webGroupFixtureDownload(router, roomAvatar, "@mallory:test.local").Code)
	// Native/federated profiles are authoritative in m.room.member state; the
	// membership index need not contain redundant avatar metadata.
	alice := "@alice:test.local"
	_, e = hs.Event.SendRoomEvent(roomID, alice, "m.room.member", nil, map[string]any{"membership": "join", "avatar_url": content["url"]}, &alice)
	require.Nil(t, e)
	memberAvatar := "/api/v1/chat/groups/" + webGroupID(roomID) + "/members/" + base64.RawURLEncoding.EncodeToString([]byte("@alice:test.local")) + "/avatar"
	require.Equal(t, 200, webGroupFixtureDownload(router, memberAvatar, "@bob:test.local").Code)
	require.Equal(t, 403, webGroupFixtureDownload(router, memberAvatar, "@mallory:test.local").Code)
}

func TestWebGroupMediaImageValidationAndSafePresentation(t *testing.T) {
	_, router, roomID := webGroupMediaTestHS(t)
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	id := webGroupFixtureUploadID(t, uploadWebGroupFixture(t, router, roomID, "@alice:test.local", "photo.png", pngData.Bytes()))
	content, e := webGroupUploadContent(db.GetDB(), roomID, "@alice:test.local", id)
	require.Nil(t, e)
	require.Equal(t, "m.image", content["msgtype"])
	oversized := bytes.Clone(pngData.Bytes())
	binary.BigEndian.PutUint32(oversized[16:20], 100000)
	binary.BigEndian.PutUint32(oversized[20:24], 100000)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	require.Equal(t, 400, uploadWebGroupFixture(t, router, roomID, "@alice:test.local", "bomb.png", oversized).Code)
	for _, raw := range [][]byte{[]byte("<html><script>alert(1)</script></html>"), []byte("<svg xmlns='http://www.w3.org/2000/svg' onload='alert(1)'/>"), []byte("\x00\x00\x00\x18ftypmp42video")} {
		response := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(response)
		c.Request = httptest.NewRequest(http.MethodGet, "/media", nil)
		serveWebGroupMediaBytes(c, bytes.NewReader(raw), int64(len(raw)), "bad\r\nname.html", false)
		require.Equal(t, 200, response.Code)
		require.Equal(t, raw, response.Body.Bytes())
		require.Equal(t, "application/octet-stream", response.Header().Get("Content-Type"))
		require.Contains(t, response.Header().Get("Content-Disposition"), "attachment")
		require.NotContains(t, response.Header().Get("Content-Disposition"), "\r")
	}
	file, err := os.CreateTemp(t.TempDir(), "fake-image")
	require.NoError(t, err)
	defer file.Close()
	_, err = file.Write([]byte("\x89PNG\r\n\x1a\ntruncated"))
	require.NoError(t, err)
	_, err = inspectWebGroupUpload(file)
	require.Error(t, err)
}

func TestWebGroupMediaCleanupKeepsReferencedFiles(t *testing.T) {
	hs, router, roomID := webGroupMediaTestHS(t)
	conn := db.GetDB()
	expired := webGroupFixtureUploadID(t, uploadWebGroupFixture(t, router, roomID, "@alice:test.local", "orphan.txt", []byte("orphan")))
	used := webGroupFixtureUploadID(t, uploadWebGroupFixture(t, router, roomID, "@alice:test.local", "used.txt", []byte("used")))
	content, e := webGroupUploadContent(conn, roomID, "@alice:test.local", used)
	require.Nil(t, e)
	_, e = hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.message", nil, content, nil)
	require.Nil(t, e)
	// Simulate a crash after persisting the event, before marking the lease.
	require.NoError(t, conn.Model(&WebGroupUpload{}).Where("id IN ?", []string{expired, used}).Update("expires_at", time.Now().Add(-time.Hour)).Error)
	var orphan, referenced WebGroupUpload
	require.NoError(t, conn.Where("id = ?", expired).First(&orphan).Error)
	require.NoError(t, conn.Where("id = ?", used).First(&referenced).Error)
	require.NoError(t, cleanupWebGroupUploads(context.Background(), hs, time.Now()))
	var n int64
	require.NoError(t, conn.Model(&MatrixMedia{}).Where("media_id = ? AND gc_state <> ?", orphan.MediaID, "deleted").Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, conn.Model(&MatrixMedia{}).Where("media_id = ?", referenced.MediaID).Count(&n).Error)
	require.EqualValues(t, 1, n, "event references must survive orphan cleanup")
	require.NoError(t, conn.Where("id = ?", used).First(&referenced).Error)
	require.Equal(t, "referenced", referenced.UsedEventID)
}

func TestWebGroupMediaEmojiUsesOnlyOwnedAssetsAndStableMXC(t *testing.T) {
	hs, _, _ := webGroupMediaTestHS(t)
	require.NoError(t, db.GetDB().AutoMigrate(&models.Emoji{}))
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	asset, err := emojiasset.StoreEmojiImage(context.Background(), bytes.NewReader(data.Bytes()), "image/png")
	require.NoError(t, err)
	emoji := models.Emoji{Shortcode: "test", Url: asset.URL, StaticUrl: asset.StaticURL}
	require.NoError(t, db.GetDB().Create(&emoji).Error)
	mxc, err := webGroupEmojiMXC(context.Background(), hs, emoji)
	require.NoError(t, err)
	second, err := webGroupEmojiMXC(context.Background(), hs, emoji)
	require.NoError(t, err)
	require.Equal(t, mxc, second)
	var count int64
	require.NoError(t, db.GetDB().Model(&MatrixMedia{}).Count(&count).Error)
	require.EqualValues(t, 1, count, "reusing an emoji must not duplicate media per message")
	server, id, err := webGroupMXC(mxc)
	require.NoError(t, err)
	_, reader, err := NewMediaService(hs).GetMedia(server, id)
	require.NoError(t, err)
	reader.Close()
	for _, update := range []map[string]any{{"local_only": true}, {"local_only": false, "disabled": true}, {"disabled": false, "domain": "external.test"}, {"domain": "", "url": "https://external.test/emoji-assets/fake.png"}} {
		require.NoError(t, db.GetDB().Model(&models.Emoji{}).Where("id = ?", emoji.Id).Updates(update).Error)
		_, err = webGroupEmojiMXC(context.Background(), hs, emoji)
		require.Error(t, err, "ineligible/custom URLs must not cause a remote fetch or bypass local-only policy")
	}
}

func TestWebGroupMediaInlineEmojisAndEditedAttachments(t *testing.T) {
	hs, router, roomID := webGroupMediaTestHS(t)
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 8, 8))))
	id := webGroupFixtureUploadID(t, uploadWebGroupFixture(t, router, roomID, "@alice:test.local", "emoji.png", data.Bytes()))
	content, e := webGroupUploadContent(db.GetDB(), roomID, "@alice:test.local", id)
	require.Nil(t, e)
	mxc := content["url"].(string)
	textContent := map[string]any{"body": ":wave:", "msgtype": "m.text", "format": "org.matrix.custom.html", "formatted_body": `<img data-mx-emoticon src="` + mxc + `" alt=":wave:"><img src="` + mxc + `" alt=":other:"><img data-mx-emoticon src="https://external.test/tracker.png" alt=":track:">`}
	sent, e := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.message", nil, textContent, nil)
	require.Nil(t, e)
	var original MatrixEvent
	require.NoError(t, db.GetDB().Where("event_id = ?", sent.EventID).First(&original).Error)
	emojis := webGroupInlineEmojis(roomID, original)
	require.Len(t, emojis, 1)
	require.Equal(t, "wave", emojis[0].Shortcode)
	require.Equal(t, 200, webGroupFixtureDownload(router, emojis[0].URL, "@alice:test.local").Code)
	edited, e := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.message", nil, map[string]any{
		"body": "* changed attachment", "msgtype": "m.text", "m.new_content": content,
		"m.relates_to": map[string]any{"rel_type": "m.replace", "event_id": original.EventID},
	}, nil)
	require.Nil(t, e)
	url := webGroupMediaURL(roomID, edited.EventID, mxc)
	require.Equal(t, 200, webGroupFixtureDownload(router, url, "@alice:test.local").Code)
	reaction, e := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.reaction", nil, map[string]any{"m.relates_to": map[string]any{"rel_type": "m.annotation", "event_id": original.EventID, "key": mxc}}, nil)
	require.Nil(t, e)
	reactionURL := webGroupMediaURL(roomID, reaction.EventID, mxc)
	require.Equal(t, 200, webGroupFixtureDownload(router, reactionURL, "@alice:test.local").Code)
	_, e = hs.Event.RedactEvent(roomID, original.EventID, "@alice:test.local", "deleted", nil)
	require.Nil(t, e)
	require.Equal(t, 404, webGroupFixtureDownload(router, url, "@alice:test.local").Code, "redacting the original also removes web access to replacement attachments")
	require.Equal(t, 404, webGroupFixtureDownload(router, reactionURL, "@alice:test.local").Code)
}
