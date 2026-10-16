package activitypub

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestRefreshRemoteAvatarDecorationsFromMisskeyAPI(t *testing.T) {
	conn := searchFederationDB(t)
	var body map[string]any
	calls, done := remoteOrigin(t, "misskey", func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/users/show" || r.Method != http.MethodPost {
			return false
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		_ = json.NewEncoder(w).Encode(map[string]any{"username": "alice", "host": nil, "avatarDecorations": []any{
			map[string]any{"id": "9abc", "url": "https://misskey.example/files/a.png", "angle": 0.75, "flipH": true, "offsetX": 0.1},
			map[string]any{"id": "bad", "url": "javascript:alert(1)"},
			map[string]any{"id": "9def", "url": "https://misskey.example/files/b.png"},
		}})
		return true
	})
	defer done()
	domain := "misskey.example"
	account := models.Account{Username: "alice", Domain: &domain, OriginId: "https://misskey.example/users/9x", DisplayName: "a"}
	require.NoError(t, conn.Create(&account).Error)

	changed, err := RefreshRemoteAvatarDecorations(context.Background(), conn, &account, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "alice", body["username"])
	var stored models.Account
	require.NoError(t, conn.First(&stored, account.Id).Error)
	require.Equal(t, models.AvatarDecorationSelections{
		{ID: "9abc", URL: "https://misskey.example/files/a.png", Angle: 0.5, FlipH: true, OffsetX: 0.1},
		{ID: "9def", URL: "https://misskey.example/files/b.png"},
	}, stored.AvatarDecorations)
	require.NotNil(t, stored.AvatarDecorationsFetchedAt)

	// Within the TTL nothing is fetched, even when forced inside a minute.
	before := *calls
	changed, err = RefreshRemoteAvatarDecorations(context.Background(), conn, &stored, true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, before, *calls)

	old := time.Now().Add(-2 * time.Minute)
	stored.AvatarDecorationsFetchedAt = &old
	changed, err = RefreshRemoteAvatarDecorations(context.Background(), conn, &stored, true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Greater(t, *calls, before)
}

func TestRefreshRemoteAvatarDecorationsSkipsNonMisskey(t *testing.T) {
	conn := searchFederationDB(t)
	calls, done := remoteOrigin(t, "mastodon", func(w http.ResponseWriter, r *http.Request) bool {
		t.Fatalf("unexpected API call %s", r.URL.Path)
		return false
	})
	defer done()
	domain := "masto.example"
	account := models.Account{Username: "bob", Domain: &domain, OriginId: "https://masto.example/users/bob", DisplayName: "b"}
	require.NoError(t, conn.Create(&account).Error)
	changed, err := RefreshRemoteAvatarDecorations(context.Background(), conn, &account, true)
	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, account.AvatarDecorationsFetchedAt)
	_ = calls
}

func TestRefreshRemoteAvatarDecorationsFailureKeepsListAndBacksOff(t *testing.T) {
	conn := searchFederationDB(t)
	_, done := remoteOrigin(t, "sharkey", func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusInternalServerError)
		return true
	})
	defer done()
	domain := "shark.example"
	prev := models.AvatarDecorationSelections{{ID: "1", URL: "https://shark.example/d.png"}}
	account := models.Account{Username: "c", Domain: &domain, OriginId: "https://shark.example/users/c", DisplayName: "c", AvatarDecorations: prev}
	require.NoError(t, conn.Create(&account).Error)
	changed, err := RefreshRemoteAvatarDecorations(context.Background(), conn, &account, true)
	require.Error(t, err)
	require.False(t, changed)
	var stored models.Account
	require.NoError(t, conn.First(&stored, account.Id).Error)
	require.Equal(t, prev, stored.AvatarDecorations)
	require.NotNil(t, stored.AvatarDecorationsFetchedAt)
}
