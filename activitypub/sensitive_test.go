package activitypub

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func sensitiveDB(t *testing.T) *gorm.DB {
	conn := emojiDB(t)
	stmt := &gorm.Statement{DB: conn}
	require.NoError(t, stmt.Parse(&models.MediaAttachment{}))
	for _, f := range stmt.Schema.Fields {
		if f.AutoIncrement {
			f.DataType = schema.Int
		}
	}
	require.NoError(t, conn.AutoMigrate(&models.MediaAttachment{}))
	return conn
}

func TestIncomingAttachmentSensitiveMarkers(t *testing.T) {
	ctx := context.Background()
	flagged := attachmentFixture("https://8.8.8.8/a.png", "image/png")
	flagged["sensitive"] = true
	flagged["blurhash"] = "LEHV6nWB2yk8pyo0adR*.7kCMdnj"
	marked := attachmentFixture("https://8.8.8.8/b.png?sensitive=true", "image/png")
	plain := attachmentFixture("https://8.8.8.8/c.png?sensitive=false", "image/png")
	got := mustIncomingAttachments(t, ctx, []any{flagged, marked, plain}, 1)
	require.Len(t, got, 3)
	assert.True(t, got[0].Sensitive)
	assert.Equal(t, "LEHV6nWB2yk8pyo0adR*.7kCMdnj", got[0].Blurhash, "remote blurhash is kept")
	assert.True(t, got[1].Sensitive, "?sensitive=true URL marker")
	assert.False(t, got[2].Sensitive)
	assert.Equal(t, "https://8.8.8.8/b.png?sensitive=true", got[1].RemoteUrl, "origin URL is not rewritten")
}

func TestDeriveSensitive(t *testing.T) {
	file := func(s bool) models.Attachments { return models.Attachments{{Sensitive: s}} }
	assert.False(t, deriveSensitive(false, "", file(false)))
	assert.True(t, deriveSensitive(true, "", nil))
	assert.True(t, deriveSensitive(false, "spoiler", nil), "a content warning implies sensitive")
	assert.False(t, deriveSensitive(false, "  ", nil))
	assert.True(t, deriveSensitive(false, "", file(true)))
}

func noteObject(id string, extra map[string]any) map[string]any {
	o := map[string]any{"id": id, "type": "Note", "content": "<p>hi</p>", "attributedTo": "https://remote.example/users/files",
		"to": []any{"https://www.w3.org/ns/activitystreams#Public"}, "cc": []any{}}
	for k, v := range extra {
		o[k] = v
	}
	return o
}

func TestSensitiveFlagOnBoostedUnknownFetch(t *testing.T) {
	conn := sensitiveDB(t)
	_, err := createRemoteAccount(conn, &Actor{ID: "https://remote.example/users/files", PreferredUsername: "files"})
	require.NoError(t, err)
	ctx := context.WithValue(context.Background(), inboxTransactionKey{}, conn)
	file := attachmentFixture("https://8.8.8.8/x.png", "image/png")

	cases := map[string]struct {
		extra map[string]any
		want  bool
	}{
		"note flag":       {map[string]any{"sensitive": true}, true},
		"content warning": {map[string]any{"summary": "cw"}, true},
		"file flag":       {map[string]any{"attachment": []any{map[string]any{"type": "Document", "url": "https://8.8.8.8/y.png?sensitive=true", "mediaType": "image/png"}}}, true},
		"nothing":         {map[string]any{"attachment": []any{file}}, false},
	}
	n := 0
	for name, tc := range cases {
		n++
		uri := "https://remote.example/notes/" + name
		st, err := importFetchedStatus(ctx, uri, noteObject(uri, tc.extra))
		require.NoError(t, err, name)
		var saved models.Status
		require.NoError(t, conn.First(&saved, st.Id).Error)
		assert.Equal(t, tc.want, saved.Sensitive, name)
		require.NotNil(t, saved.Domain, name+": remote status domain is set")
		assert.Equal(t, "remote.example", *saved.Domain, name)
	}
	_ = n
}

func TestSensitiveFlagOnRemoteUpdate(t *testing.T) {
	conn := sensitiveDB(t)
	actorURI := "https://remote.example/users/files"
	actor, err := createRemoteAccount(conn, &Actor{ID: actorURI, PreferredUsername: "files"})
	require.NoError(t, err)
	domain, uri := "remote.example", "https://remote.example/notes/upd"
	status := models.Status{AccountId: actor.Id, Domain: &domain, OriginUri: &uri, Content: models.StatusContent{Content: "Old"}}
	require.NoError(t, persistIncomingStatusWithEmoji(context.Background(), conn, &status, nil))
	update := func(extra map[string]any) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		ctx := context.WithValue(context.WithValue(context.Background(), actorKey, actorURI), inboxTransactionKey{}, conn)
		obj := map[string]any{"id": uri, "type": "Note", "content": "Old"}
		for k, v := range extra {
			obj[k] = v
		}
		handleUpdateActivity(ctx, c, map[string]any{"object": obj}, nil)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	flag := func() bool {
		var s models.Status
		require.NoError(t, conn.First(&s, status.Id).Error)
		return s.Sensitive
	}
	update(nil)
	assert.False(t, flag())
	update(map[string]any{"sensitive": true})
	assert.True(t, flag())
	update(map[string]any{"sensitive": false, "attachment": []any{}})
	assert.False(t, flag(), "the origin's edit can clear it")
	update(map[string]any{"summary": "now with a warning"})
	assert.True(t, flag())
	update(map[string]any{"attachment": []any{map[string]any{"type": "Document", "url": "https://8.8.8.8/z.png", "mediaType": "image/png", "sensitive": true}}})
	assert.True(t, flag())
}

type flagsFetcher struct{ obj map[string]any }

func (f flagsFetcher) FetchActor(context.Context, string) (*Actor, error) { return nil, nil }
func (f flagsFetcher) FetchObject(context.Context, string) (map[string]any, error) {
	return f.obj, nil
}

func TestRefreshStatusFlags(t *testing.T) {
	conn := sensitiveDB(t)
	actor, err := createRemoteAccount(conn, &Actor{ID: "https://remote.example/users/files", PreferredUsername: "files"})
	require.NoError(t, err)
	domain, uri := "remote.example", "https://remote.example/notes/lost"
	status := models.Status{AccountId: actor.Id, Domain: &domain, OriginUri: &uri, Content: models.StatusContent{Content: "x"},
		Attachments: models.Attachments{{AccountId: actor.Id, FileName: "a.png", RemoteUrl: "https://8.8.8.8/a.png?sensitive=true", ContentType: "image/png", Type: models.MediaTypeImage}}}
	require.NoError(t, persistIncomingStatusWithEmoji(context.Background(), conn, &status, nil))

	obj := noteObject(uri, map[string]any{"sensitive": true, "attachment": []any{
		map[string]any{"type": "Document", "mediaType": "image/png", "url": "https://8.8.8.8/a.png?sensitive=true", "sensitive": true, "blurhash": "LEHV6nWB2yk8pyo0adR*.7kCMdnj"},
	}})
	var loaded models.Status
	require.NoError(t, conn.First(&loaded, status.Id).Error)

	res, err := RefreshStatusFlags(context.Background(), conn, flagsFetcher{obj}, &loaded, true)
	require.NoError(t, err)
	assert.True(t, res.SensitiveChanged)
	assert.Equal(t, 1, res.FilesUpdated)
	var again models.Status
	require.NoError(t, conn.Preload("Attachments").First(&again, status.Id).Error)
	assert.False(t, again.Sensitive, "dry run writes nothing")
	assert.Empty(t, again.Attachments[0].Blurhash)

	res, err = RefreshStatusFlags(context.Background(), conn, flagsFetcher{obj}, &loaded, false)
	require.NoError(t, err)
	require.NoError(t, conn.Preload("Attachments").First(&again, status.Id).Error)
	assert.True(t, again.Sensitive)
	assert.True(t, again.Attachments[0].Sensitive)
	assert.Equal(t, "LEHV6nWB2yk8pyo0adR*.7kCMdnj", again.Attachments[0].Blurhash)
	api, err := serializer.ToStatusBase(&again)
	require.NoError(t, err)
	assert.True(t, api.Sensitive)
	assert.NotNil(t, api.Tags)

	// A different object id is refused, a tombstone is reported.
	_, err = RefreshStatusFlags(context.Background(), conn, flagsFetcher{noteObject("https://evil.example/n", nil)}, &again, false)
	assert.Error(t, err)
	res, err = RefreshStatusFlags(context.Background(), conn, flagsFetcher{map[string]any{"id": uri, "type": "Tombstone"}}, &again, false)
	require.NoError(t, err)
	assert.True(t, res.Gone)
}
