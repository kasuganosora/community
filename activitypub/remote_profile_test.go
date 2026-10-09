package activitypub

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRemoteProfileFieldsSanitized(t *testing.T) {
	actor := &Actor{Attachments: []Attachment{
		{Type: "PropertyValue", Name: "Blog <b>x</b>", Value: `<a href="https://blog.example" onclick="x()">blog.example</a><script>alert(1)</script>`},
		{Type: "Image", Name: "ignored", Value: "ignored"},
		{Type: "PropertyValue", Name: "", Value: ""},
	}}
	fields := remoteProfileFields(actor)
	require.Len(t, fields, 1)
	require.Equal(t, "Blog x", fields[0].Name)
	require.Contains(t, fields[0].Val, `href="https://blog.example"`)
	require.NotContains(t, fields[0].Val, "onclick")
	require.NotContains(t, fields[0].Val, "<script")

	many := &Actor{}
	for range 40 {
		many.Attachments = append(many.Attachments, Attachment{Type: "PropertyValue", Name: "n", Value: strings.Repeat("v", 5000)})
	}
	fields = remoteProfileFields(many)
	require.Len(t, fields, maxRemoteProfileFields)
	require.LessOrEqual(t, len([]rune(fields[0].Val)), maxRemoteFieldValue)
}

type fakeCollections map[string]map[string]any

func (f fakeCollections) FetchObject(_ context.Context, uri string) (map[string]any, error) {
	if obj, ok := f[uri]; ok {
		return obj, nil
	}
	return nil, errors.New("not found")
}

func TestSyncRemoteCollectionCounts(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, conn.AutoMigrate(&models.AccountExtra{}))
	require.NoError(t, conn.Create(&models.AccountExtra{AccountId: 7, FollowersCount: 1, FollowingCount: 2, StatusesCount: 3}).Error)

	actor := &Actor{
		ID:        "https://remote.example/users/alice",
		Followers: "https://remote.example/users/alice/followers",
		Following: "https://remote.example/users/alice/following",
		Outbox:    "https://other.example/users/alice/outbox", // foreign host: ignored
	}
	client := fakeCollections{
		actor.Followers: {"type": "OrderedCollection", "totalItems": float64(1234)},
		// Hidden collection without totalItems leaves the counter alone.
		actor.Following: {"type": "OrderedCollection"},
		actor.Outbox:    {"totalItems": float64(99)},
	}
	require.NoError(t, syncRemoteCollectionCounts(context.Background(), conn, client, 7, actor))

	var extra models.AccountExtra
	require.NoError(t, conn.First(&extra, "account_id = ?", 7).Error)
	require.Equal(t, 1234, extra.FollowersCount)
	require.Equal(t, 2, extra.FollowingCount)
	require.Equal(t, 3, extra.StatusesCount)
}
