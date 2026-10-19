package community

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/stretchr/testify/require"
)

func newPrivacyFixture(t *testing.T) (discoveryRouteFixture, models.Account) {
	f := newDiscoveryRouteFixture(t)
	require.NoError(t, f.conn.AutoMigrate(&models.Follow{}, &models.Status{}))
	third := models.Account{Id: 8301, Username: "third"}
	user := models.User{Id: 8103, UUID: "privacy-third", UserName: "third", EmailHash: "privacy-third-email"}
	require.NoError(t, f.conn.Create(&user).Error)
	third.UserId = user.Id
	require.NoError(t, f.conn.Create(&third).Error)
	// other follows owner; owner follows third.
	require.NoError(t, f.conn.Create(&models.Follow{AccountId: f.other.Id, FollowId: f.owner.Id, Status: models.FollowStatusFollowed}).Error)
	require.NoError(t, f.conn.Create(&models.Follow{AccountId: f.owner.Id, FollowId: third.Id, Status: models.FollowStatusFollowed}).Error)
	return f, third
}

func listLen(t *testing.T, f discoveryRouteFixture, path, token string) int {
	t.Helper()
	w := f.request("GET", path, token, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var out []map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return len(out)
}

func TestCollectionVisibilityMastodonAPI(t *testing.T) {
	f, third := newPrivacyFixture(t)
	read := uint64(models.AppScopeRead)
	owner, other, thirdTok := f.token(t, f.owner, read, 0), f.token(t, f.other, read, 0), f.token(t, third, read, 0)
	followers, following := "/api/v1/accounts/8201/followers", "/api/v1/accounts/8201/following"
	set := func(fing, fers string) {
		require.NoError(t, f.conn.Model(&models.Account{}).Where("id = ?", f.owner.Id).Updates(models.CollectionVisibilityUpdates(fing, fers)).Error)
	}
	require.Equal(t, 1, listLen(t, f, followers, ""))
	require.Equal(t, 1, listLen(t, f, following, ""))

	set(models.CollectionFollowers, models.CollectionFollowers)
	require.Equal(t, 0, listLen(t, f, followers, ""), "anonymous")
	require.Equal(t, 0, listLen(t, f, followers, thirdTok), "not a follower")
	require.Equal(t, 1, listLen(t, f, followers, other), "follower")
	require.Equal(t, 1, listLen(t, f, following, other), "follower sees following")
	require.Equal(t, 1, listLen(t, f, followers, owner), "owner")

	set(models.CollectionPublic, models.CollectionPrivate)
	require.Equal(t, 0, listLen(t, f, followers, other))
	require.Equal(t, 1, listLen(t, f, following, ""), "following stays public")
	require.Equal(t, 1, listLen(t, f, followers, owner))

	// hide_collections reads true if either list is not public.
	var acc models.Account
	require.NoError(t, f.conn.First(&acc, f.owner.Id).Error)
	require.True(t, serializer.ToAccount(&acc).HideCollections)
	require.Equal(t, models.CollectionPrivate, serializer.ToAccount(&acc).FollowersVisibility)
}

func TestCollectionVisibilityActivityPub(t *testing.T) {
	f, _ := newPrivacyFixture(t)
	get := func(path string) (int, map[string]any) {
		req, _ := http.NewRequest("GET", path, nil)
		req.Header.Set("Accept", "application/activity+json")
		w := f.request("GET", path, "", "")
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	code, body := get("/users/" + f.owner.Username + "/followers")
	require.Equal(t, 200, code)
	require.EqualValues(t, 1, body["totalItems"])
	require.NotEmpty(t, body["first"])
	code, _ = get("/users/" + f.owner.Username + "/followers?page=true")
	require.Equal(t, 200, code)

	require.NoError(t, f.conn.Model(&models.Account{}).Where("id = ?", f.owner.Id).Updates(models.CollectionVisibilityUpdates(models.CollectionPublic, models.CollectionFollowers)).Error)
	code, body = get("/users/" + f.owner.Username + "/followers")
	require.Equal(t, 200, code)
	require.EqualValues(t, 1, body["totalItems"], "size stays visible")
	require.Nil(t, body["first"], "no first page")
	code, _ = get("/users/" + f.owner.Username + "/followers?page=true")
	require.Equal(t, 403, code)
	code, body = get("/users/" + f.owner.Username + "/following")
	require.Equal(t, 200, code)
	require.NotEmpty(t, body["first"], "following still public")
}

func TestAutoAcceptFollowedAndOnlineStatus(t *testing.T) {
	f, third := newPrivacyFixture(t)
	require.NoError(t, f.conn.Model(&models.Account{}).Where("id = ?", f.owner.Id).Updates(map[string]any{"locked": true, "auto_accept_followed": true}).Error)
	var owner models.Account
	require.NoError(t, f.conn.First(&owner, f.owner.Id).Error)
	require.True(t, owner.AutoAcceptsFollowFrom(f.conn, &third), "owner follows third")
	require.False(t, owner.AutoAcceptsFollowFrom(f.conn, &f.other), "owner does not follow other")
	owner.AutoAcceptFollowed = false
	require.False(t, owner.AutoAcceptsFollowFrom(f.conn, &third))

	// Local follow request through the API: auto-approved for third.
	require.NoError(t, f.conn.Where("account_id = ? AND follow_id = ?", third.Id, f.owner.Id).Delete(&models.Follow{}).Error)
	w := f.request("POST", "/api/v1/accounts/8201/follow", f.token(t, third, models.AppScopeRead, models.AppScopeWrite), `{}`)
	require.Less(t, w.Code, 300, w.Body.String())
	var follow models.Follow
	require.NoError(t, f.conn.Where("account_id = ? AND follow_id = ?", third.Id, f.owner.Id).First(&follow).Error)
	require.Equal(t, models.FollowStatusFollowed, follow.Status)

	// Online status thresholds and the hide flag.
	now := time.Now()
	at := func(d time.Duration) *time.Time { v := now.Add(-d); return &v }
	a := models.Account{}
	require.Equal(t, "unknown", a.OnlineStatus(now))
	a.LastActiveAt = at(time.Minute)
	require.Equal(t, "online", a.OnlineStatus(now))
	a.LastActiveAt = at(2 * time.Hour)
	require.Equal(t, "active", a.OnlineStatus(now))
	a.LastActiveAt = at(4 * 24 * time.Hour)
	require.Equal(t, "offline", a.OnlineStatus(now))
	a.HideOnlineStatus = true
	require.Equal(t, "unknown", a.OnlineStatus(now))

	// Authenticated requests record activity, throttled.
	tok := f.token(t, f.other, models.AppScopeRead, 0)
	f.request("GET", "/api/v1/accounts/verify_credentials", tok, "")
	var other models.Account
	require.NoError(t, f.conn.First(&other, f.other.Id).Error)
	require.NotNil(t, other.LastActiveAt)
	first := *other.LastActiveAt
	f.request("GET", "/api/v1/accounts/verify_credentials", tok, "")
	require.NoError(t, f.conn.First(&other, f.other.Id).Error)
	require.True(t, first.Equal(*other.LastActiveAt), "throttled")
	require.Equal(t, "online", serializer.ToAccount(&other).OnlineStatus)
}
