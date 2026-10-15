package admin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedInstances(t *testing.T, conn *gorm.DB) {
	t.Helper()
	s := conn.Session(&gorm.Session{SkipHooks: true})
	old, recent := time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour)
	for i, d := range []string{"alpha.example", "beta.example", "beta.example", "gamma.example"} {
		dom := d
		ts := old
		if i > 0 {
			ts = recent
		}
		require.NoError(t, s.Create(&models.Account{Id: uint64(10 + i), Username: "u", Domain: &dom, OriginId: "https://" + d + "/u/" + string(rune('a'+i)), CreatedAt: &ts, LastActivityAt: &ts}).Error)
	}
	// local(1) follows alpha; beta follows local; gamma none.
	require.NoError(t, s.Create(&models.Follow{Id: 1, AccountId: 1, FollowId: 10, Status: models.FollowStatusFollowed}).Error)
	require.NoError(t, s.Create(&models.Follow{Id: 2, AccountId: 11, FollowId: 1, Status: models.FollowStatusFollowed}).Error)
	require.NoError(t, s.Create(&models.Follow{Id: 3, AccountId: 12, FollowId: 1, Status: models.FollowStatusFollowed}).Error)
	now := time.Now()
	require.NoError(t, conn.Create(&models.InstanceMetadata{Domain: "alpha.example", Name: "Alpha", Software: "misskey", Version: "2025.1.0", IconURL: "https://alpha.example/icon.png", FetchedAt: &now}).Error)
	require.NoError(t, conn.Create(&models.InstanceMetadata{Domain: "gamma.example", FailCount: 2, FailedAt: &now}).Error)
}

type instancesResult struct {
	Items []struct {
		Domain, Name, Software, Version, Status string
		IconURL                                 string `json:"icon_url"`
		Accounts, Following, Followers          int64
	}
	Total int64
}

func getInstances(t *testing.T, a *models.Account, query string) (int, instancesResult) {
	c, w := newGetContext(t, nil, query)
	c.Set("account", a)
	FederationInstances(c)
	var r instancesResult
	if w.Code == 200 {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &r))
	}
	return w.Code, r
}

func TestFederationInstancesMetadataFilterSort(t *testing.T) {
	conn, a := operationsDB(t)
	seedInstances(t, conn)
	code, r := getInstances(t, a, "")
	require.Equal(t, 200, code)
	require.EqualValues(t, 3, r.Total)
	require.Equal(t, "alpha.example", r.Items[0].Domain)
	require.Equal(t, "Alpha", r.Items[0].Name)
	require.Equal(t, "misskey", r.Items[0].Software)
	require.NotEmpty(t, r.Items[0].IconURL)
	require.Equal(t, "subscribing", r.Items[0].Status)
	require.Equal(t, "publishing", r.Items[1].Status)
	require.Equal(t, "failing", r.Items[2].Status)

	for status, want := range map[string]string{"subscribing": "alpha.example", "publishing": "beta.example", "failing": "gamma.example"} {
		code, r = getInstances(t, a, "status="+status)
		require.Equal(t, 200, code)
		require.Len(t, r.Items, 1, status)
		require.Equal(t, want, r.Items[0].Domain)
	}
	_, r = getInstances(t, a, "status=federating")
	require.EqualValues(t, 2, r.Total)

	_, r = getInstances(t, a, "sort=pubsub_desc")
	require.Equal(t, "beta.example", r.Items[0].Domain)
	require.EqualValues(t, 2, r.Items[0].Followers)
	_, r = getInstances(t, a, "sort=accounts_desc")
	require.Equal(t, "beta.example", r.Items[0].Domain)
	_, r = getInstances(t, a, "sort=first_seen_asc")
	require.Equal(t, "alpha.example", r.Items[0].Domain)
	_, r = getInstances(t, a, "q=gam")
	require.Len(t, r.Items, 1)

	code, _ = getInstances(t, a, "sort=bogus")
	require.Equal(t, 422, code)
	code, _ = getInstances(t, a, "status=bogus")
	require.Equal(t, 422, code)
}

func TestFederationInstanceActivity(t *testing.T) {
	conn, a := operationsDB(t)
	seedInstances(t, conn)
	now := time.Now()
	s := conn.Session(&gorm.Session{SkipHooks: true})
	for i := 0; i < 3; i++ {
		require.NoError(t, s.Create(&models.Status{Id: uint64(100 + i), AccountId: 11, CreatedAt: &now}).Error)
	}
	instanceActivityCache = map[string]activityCacheEntry{}
	c, w := newGetContext(t, nil, "domains=beta.example,alpha.example")
	c.Set("account", a)
	FederationInstanceActivity(c)
	require.Equal(t, 200, w.Code, w.Body.String())
	var r struct {
		Days  int
		Items map[string][]int64
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &r))
	require.Len(t, r.Items["beta.example"], 14)
	require.EqualValues(t, 3, r.Items["beta.example"][13])
	require.EqualValues(t, 0, r.Items["alpha.example"][13])

	c, w = newGetContext(t, nil, "domains=bad/host")
	c.Set("account", a)
	FederationInstanceActivity(c)
	require.Equal(t, 422, w.Code)
}
