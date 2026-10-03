package activitypub

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func emojiDB(t *testing.T) *gorm.DB {
	conn := persistenceDB(t, false)
	stmt := &gorm.Statement{DB: conn}
	require.NoError(t, stmt.Parse(&models.Emoji{}))
	for _, f := range stmt.Schema.Fields {
		if f.AutoIncrement {
			f.DataType = schema.Int
		}
	}
	// Base persistence fixture created this association implicitly before its
	// integer primary key was normalised for SQLite.
	require.NoError(t, conn.Migrator().DropTable(&models.Emoji{}))
	require.NoError(t, conn.AutoMigrate(&models.Emoji{}))
	return conn
}
func sampleEmojiTags(url string) []Tag {
	return []Tag{{Type: "Emoji", Name: ":party:", ID: "https://unrelated.example/emojis/party", Icon: &Image{Type: "Image", URL: url, MediaType: "image/png"}, MisskeyLicense: &EmojiLicense{FreeText: "CC0"}}}
}
func TestFederatedEmojiNamesAreAuthorScoped(t *testing.T) {
	conn := emojiDB(t)
	local := models.Emoji{Shortcode: "party", Url: "https://local.example/party.png", StaticUrl: "https://local.example/party.png"}
	require.NoError(t, conn.Create(&local).Error)
	a, err := importEmojiTags(conn, "one.example", "hello :party:", sampleEmojiTags("https://cdn.example/one.png"))
	require.NoError(t, err)
	require.Len(t, a, 1)
	b, err := importEmojiTags(conn, "two.example", "hello :party:", sampleEmojiTags("https://cdn.example/two.png"))
	require.NoError(t, err)
	require.Len(t, b, 1)
	require.NotEqual(t, a[0].Id, b[0].Id)
	require.NotEqual(t, local.Id, a[0].Id)
	require.Equal(t, "one.example", a[0].Domain)
	require.Equal(t, "CC0", a[0].License)
	again, err := importEmojiTags(conn, "one.example", ":party:", sampleEmojiTags("https://cdn.example/revised.png"))
	require.NoError(t, err)
	require.Equal(t, a[0].Id, again[0].Id)
	require.Equal(t, "https://cdn.example/revised.png", again[0].Url)
	found, err := models.GetEmojiFromText(conn, ":party:")
	require.NoError(t, err)
	require.Equal(t, local.Id, found["party"].Id)
}
func TestFederatedEmojiRejectsUnusedAndUnsafeTags(t *testing.T) {
	conn := emojiDB(t)
	for _, tc := range []struct{ text, url string }{
		{`<a href="https://example/:party:">link</a>`, "https://cdn.example/p.png"},
		{`<code>:party:</code>`, "https://cdn.example/p.png"},
		{":party:", "javascript:alert(1)"}, {":party:", "http://127.0.0.1/p.png"}, {":party:", "http://[::1]/p.png"},
	} {
		got, err := importEmojiTags(conn, "remote.example", tc.text, sampleEmojiTags(tc.url))
		require.NoError(t, err)
		require.Empty(t, got)
	}
}
func TestFederatedEmojiExportsOnlyUsedPublicLocalTags(t *testing.T) {
	emojis := []*models.Emoji{
		{Shortcode: "party", Url: "https://local.example/p.png", ContentType: "image/png", License: "CC0"},
		{Shortcode: "private", Url: "https://local.example/private.png", LocalOnly: true},
		{Shortcode: "disabled", Url: "https://local.example/disabled.png", Disabled: true},
		{Shortcode: "unused", Url: "https://local.example/unused.png"},
		{Shortcode: "party", Url: "https://remote.example/p.png", Domain: "remote.example"},
	}
	tags := emojiTags(emojis, ":party: :private: :disabled:", "https://local.example")
	require.Len(t, tags, 1)
	require.Equal(t, ":party:", tags[0].Name)
	require.Equal(t, "https://local.example/emojis/party", tags[0].ID)
	require.Equal(t, "CC0", tags[0].MisskeyLicense.FreeText)
	require.Equal(t, "image/png", tags[0].Icon.MediaType)
}
func TestRemoteStatusPersistsOnlyAuthenticatedEmojiTags(t *testing.T) {
	conn := emojiDB(t)
	actor, err := createRemoteAccount(conn, &Actor{ID: "https://one.example/users/author", PreferredUsername: "author"})
	require.NoError(t, err)
	local := models.Emoji{Shortcode: "party", Url: "https://local.example/p.png", StaticUrl: "https://local.example/p.png"}
	require.NoError(t, conn.Create(&local).Error)
	domain := "one.example"
	uri := "https://one.example/notes/emoji"
	status := models.Status{AccountId: actor.Id, Domain: &domain, OriginUri: &uri, Content: models.StatusContent{Content: ":party:"}}
	require.NoError(t, persistIncomingStatusWithEmoji(context.Background(), conn, &status, sampleEmojiTags("https://cdn.example/one.png")))
	var saved models.Status
	require.NoError(t, conn.Preload("Emojis").First(&saved, status.Id).Error)
	require.Len(t, saved.Emojis, 1)
	require.Equal(t, domain, saved.Emojis[0].Domain)
	require.NotEqual(t, local.Id, saved.Emojis[0].Id)
	noTags := models.Status{AccountId: actor.Id, Domain: &domain, Content: models.StatusContent{Content: ":party:"}}
	require.NoError(t, persistIncomingStatusWithEmoji(context.Background(), conn, &noTags, nil))
	var links int64
	require.NoError(t, conn.Model(&models.StatusEmoji{}).Where("status_id = ?", noTags.Id).Count(&links).Error)
	require.Zero(t, links)
}
func TestRemoteActorEmojiAssociationRefresh(t *testing.T) {
	conn := emojiDB(t)
	actor, err := createRemoteAccount(conn, &Actor{ID: "https://one.example/users/profile", PreferredUsername: "profile", Name: "Profile :party:", Tag: sampleEmojiTags("https://cdn.example/profile.png")})
	require.NoError(t, err)
	require.Len(t, actor.Emojis, 1)
	require.NoError(t, importAccountEmojiTags(conn, &actor, nil))
	require.Empty(t, actor.Emojis)
	require.Zero(t, conn.Model(&actor).Association("Emojis").Count())
}

func TestRemoteEmojiUpdateChangesAssociationsAtomically(t *testing.T) {
	conn := emojiDB(t)
	actor, err := createRemoteAccount(conn, &Actor{ID: "https://one.example/users/updater", PreferredUsername: "updater"})
	require.NoError(t, err)
	domain := "one.example"
	uri := "https://one.example/notes/update-emoji"
	status := models.Status{AccountId: actor.Id, Domain: &domain, OriginUri: &uri, Content: models.StatusContent{Content: ":party:"}}
	require.NoError(t, persistIncomingStatusWithEmoji(context.Background(), conn, &status, sampleEmojiTags("https://cdn.example/one.png")))
	ctx := context.WithValue(context.Background(), actorKey, actor.OriginId)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	handleUpdateActivity(ctx, c, map[string]any{"object": map[string]any{"id": uri, "type": "Note", "content": ":party:", "tag": []any{}}}, nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	var saved models.Status
	require.NoError(t, conn.Preload("Emojis").First(&saved, status.Id).Error)
	require.Empty(t, saved.Emojis)
	// A different signing actor cannot change the association or its URL.
	badCtx := context.WithValue(context.Background(), actorKey, "https://evil.example/users/attacker")
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	handleUpdateActivity(badCtx, c, map[string]any{"object": map[string]any{"id": uri, "type": "Note", "content": ":party:", "tag": sampleEmojiTags("https://evil.example/p.png")}}, nil)
	require.Equal(t, 403, w.Code)
	var emoji models.Emoji
	require.NoError(t, conn.Where("domain = ? AND shortcode = ?", domain, "party").First(&emoji).Error)
	require.Equal(t, "https://cdn.example/one.png", emoji.Url)
}

func TestEmojiImportIgnoresMalformedNeighborAndRollsBack(t *testing.T) {
	conn := emojiDB(t)
	tags := []any{"https://example/tag", map[string]any{"type": "Emoji", "name": ":bad:", "icon": []any{}}, sampleEmojiTags("https://cdn.example/p.png")[0]}
	require.Error(t, conn.Transaction(func(tx *gorm.DB) error {
		got, err := importEmojiTags(tx, "remote.example", ":party: :bad:", tags)
		require.NoError(t, err)
		require.Len(t, got, 1)
		return gorm.ErrInvalidTransaction
	}))
	var count int64
	require.NoError(t, conn.Model(&models.Emoji{}).Where("domain = ?", "remote.example").Count(&count).Error)
	require.Zero(t, count)
}
