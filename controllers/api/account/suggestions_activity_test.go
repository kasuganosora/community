package account

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/discovery"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func activityNames(rows []discovery.AccountSuggestion) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Account.Username)
	}
	return out
}

func activityPost(t *testing.T, conn *gorm.DB, accountID uint64, at time.Time, visibility uint8, edit func(*models.Status)) {
	t.Helper()
	posted := at
	status := &models.Status{
		AccountId:  accountID,
		Visibility: visibility,
		CreatedAt:  &posted,
		UpdatedAt:  &posted,
	}
	if edit != nil {
		edit(status)
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Content").Create(status).Error)
}

func TestSuggestionActivityUsesStoredPublicPosts(t *testing.T) {
	conn, existing := suggestionFixture(t)
	require.NoError(t, conn.Session(&gorm.Session{AllowGlobalUpdate: true, SkipHooks: true}).Delete(&models.Follow{}).Error)
	require.NoError(t, conn.Where("id <> ?", existing[0].Id).Delete(&models.Account{}).Error)

	asOf := time.Now().UTC().Truncate(time.Second)
	cutoff := asOf.Add(-14 * 24 * time.Hour)
	freshLast := asOf
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	yes := true
	remoteDomain := "remote.example"
	mirrorDomain := "mirror.example"

	type spec struct {
		name   string
		domain *string
		remote bool
		last   *time.Time
		seen   *time.Time
		post   func(id uint64)
		want   bool
	}
	cases := []spec{
		{name: "fresh", last: &freshLast, want: true, post: func(id uint64) {
			activityPost(t, conn, id, asOf.Add(-time.Hour), models.StatusVisibilityPublic, nil)
		}},
		{name: "cutoff", want: true, post: func(id uint64) {
			activityPost(t, conn, id, cutoff, models.StatusVisibilityPublic, nil)
		}},
		{name: "before-cutoff", last: &freshLast, post: func(id uint64) {
			activityPost(t, conn, id, cutoff.Add(-time.Second), models.StatusVisibilityPublic, nil)
		}},
		{name: "future", last: &freshLast, post: func(id uint64) {
			activityPost(t, conn, id, asOf.Add(time.Second), models.StatusVisibilityPublic, nil)
		}},
		{name: "never", last: &freshLast},
		{name: "private", last: &freshLast, post: func(id uint64) {
			activityPost(t, conn, id, asOf.Add(-time.Hour), models.StatusVisibilityPrivate, nil)
		}},
		{name: "unlisted", last: &freshLast, post: func(id uint64) {
			activityPost(t, conn, id, asOf.Add(-time.Hour), models.StatusVisibilityUnlisted, nil)
		}},
		{name: "deleted", last: &freshLast, post: func(id uint64) {
			activityPost(t, conn, id, asOf.Add(-time.Hour), models.StatusVisibilityPublic, func(s *models.Status) {
				gone := asOf
				s.DeletedAt = &gone
			})
		}},
		{name: "scheduled", last: &freshLast, post: func(id uint64) {
			activityPost(t, conn, id, asOf.Add(-time.Hour), models.StatusVisibilityPublic, func(s *models.Status) {
				when := asOf.Add(time.Hour)
				s.ScheduledAt = &when
			})
		}},
		{name: "reply", want: true, post: func(id uint64) {
			parent := uint64(900001)
			activityPost(t, conn, id, asOf.Add(-2*time.Hour), models.StatusVisibilityPublic, func(s *models.Status) {
				s.InReplyToId = &parent
			})
		}},
		{name: "booster", last: &freshLast, post: func(id uint64) {
			original := uint64(900002)
			activityPost(t, conn, id, asOf.Add(-time.Hour), models.StatusVisibilityPublic, func(s *models.Status) {
				s.ReblogId = &original
			})
		}},
		{name: "boosted-author", last: &freshLast, post: func(id uint64) {
			activityPost(t, conn, id, cutoff.Add(-48*time.Hour), models.StatusVisibilityPublic, nil)
		}},
		{name: "imported-old", last: &freshLast, post: func(id uint64) {
			activityPost(t, conn, id, cutoff.Add(-30*24*time.Hour), models.StatusVisibilityPublic, func(s *models.Status) {
				s.UpdatedAt = &asOf
				s.MarkBackfilled()
			})
		}},
		{name: "imported-recent", last: &freshLast, post: func(id uint64) {
			activityPost(t, conn, id, asOf.Add(-time.Hour), models.StatusVisibilityPublic, func(s *models.Status) {
				s.MarkBackfilled()
			})
		}},
		{name: "mirror", domain: &mirrorDomain, remote: true, last: &freshLast, post: func(id uint64) {
			origin := base + "/users/local/statuses/15"
			activityPost(t, conn, id, asOf.Add(-time.Hour), models.StatusVisibilityPublic, func(s *models.Status) {
				s.OriginUri = &origin
				s.Domain = &mirrorDomain
			})
		}},
		{name: "remote-recent", domain: &remoteDomain, remote: true, want: true, post: func(id uint64) {
			origin := "https://remote.example/users/remote-recent/statuses/9"
			activityPost(t, conn, id, asOf.Add(-3*time.Hour), models.StatusVisibilityPublic, func(s *models.Status) {
				s.OriginUri = &origin
				s.Domain = &remoteDomain
			})
		}},
		{name: "remote-stale", domain: &remoteDomain, remote: true, last: &freshLast, seen: &freshLast, post: func(id uint64) {
			origin := "https://remote.example/users/remote-stale/statuses/1"
			activityPost(t, conn, id, cutoff.Add(-24*time.Hour), models.StatusVisibilityPublic, func(s *models.Status) {
				s.OriginUri = &origin
				s.Domain = &remoteDomain
				s.UpdatedAt = &asOf
			})
		}},
	}

	var staleID uint64
	var staleBefore models.Account
	for i, tc := range cases {
		u := &models.User{UserName: tc.name, EmailHash: tc.name, Status: models.UserStatusNormal}
		if !tc.remote {
			require.NoError(t, conn.Create(u).Error)
		}
		account := &models.Account{
			Id:           uint64(100 + i),
			Username:     tc.name,
			Acct:         tc.name,
			DisplayName:  tc.name,
			Discoverable: true,
			Avatar:       "https://cdn.example/media/" + tc.name + ".png",
			LastStatusAt: tc.last,
			Domain:       tc.domain,
		}
		if tc.seen != nil {
			account.LastActivityAt = tc.seen
			account.FeedLastSeenAt = tc.seen
		}
		if tc.remote {
			account.Acct = tc.name + "@" + *tc.domain
			account.OriginId = "https://" + *tc.domain + "/users/" + tc.name
			account.RemoteDiscoverable = &yes
			account.RemoteIndexable = &yes
			checked := time.Now().UTC()
			account.RemoteDiscoveryCheckedAt = &checked
		} else {
			account.UserId = u.Id
		}
		require.NoError(t, conn.Omit("Extra", "User").Create(account).Error)
		if tc.post != nil {
			tc.post(account.Id)
		}
		if tc.name == "remote-stale" {
			staleID = account.Id
			require.NoError(t, conn.First(&staleBefore, staleID).Error)
		}
	}

	rows, err := discovery.SuggestAccounts(t.Context(), conn, existing[0], 40, discovery.SuggestOptions{Now: asOf})
	require.NoError(t, err)
	got := map[string]bool{}
	for _, name := range activityNames(rows) {
		got[name] = true
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, got[tc.name], tc.name)
	}

	var feedback int64
	require.NoError(t, conn.Model(&models.RecommendationFeedback{}).Count(&feedback).Error)
	require.Zero(t, feedback, "exclusion must not be stored as feedback or a dead-account flag")
	var staleAfter models.Account
	require.NoError(t, conn.First(&staleAfter, staleID).Error)
	require.True(t, staleBefore.LastStatusAt.Equal(*staleAfter.LastStatusAt))
	require.True(t, staleBefore.UpdatedAt.Equal(*staleAfter.UpdatedAt))

	activityPost(t, conn, staleID, asOf.Add(-time.Minute), models.StatusVisibilityPublic, func(s *models.Status) {
		origin := "https://remote.example/users/remote-stale/statuses/2"
		s.OriginUri = &origin
		s.Domain = &remoteDomain
	})
	rows, err = discovery.SuggestAccounts(t.Context(), conn, existing[0], 40, discovery.SuggestOptions{Now: asOf})
	require.NoError(t, err)
	require.Contains(t, activityNames(rows), "remote-stale", "a later stored public post is eligible without a permanent exclusion")
}

func TestSuggestionActivityRevalidatesCutoffAndDeletion(t *testing.T) {
	conn, existing := suggestionFixture(t)
	require.NoError(t, conn.Session(&gorm.Session{AllowGlobalUpdate: true, SkipHooks: true}).Delete(&models.Follow{}).Error)
	require.NoError(t, conn.Where("id <> ?", existing[0].Id).Delete(&models.Account{}).Error)
	asOf := time.Now().UTC().Truncate(time.Second)
	posted := asOf.Add(-14 * 24 * time.Hour)
	suggestionUser(t, conn, "boundary", "https://cdn.example/media/boundary.png", posted)
	var account models.Account
	require.NoError(t, conn.Where("username = ?", "boundary").First(&account).Error)
	activityPost(t, conn, account.Id, posted, models.StatusVisibilityPublic, nil)

	rows, err := discovery.SuggestAccounts(t.Context(), conn, existing[0], 10, discovery.SuggestOptions{Now: posted.Add(14 * 24 * time.Hour)})
	require.NoError(t, err)
	require.Equal(t, []string{"boundary"}, activityNames(rows))

	rows, err = discovery.SuggestAccounts(t.Context(), conn, existing[0], 10, discovery.SuggestOptions{Now: posted.Add(14*24*time.Hour + time.Second)})
	require.NoError(t, err)
	require.Empty(t, rows, "the next read applies the new cutoff to the same stored post")

	rows, err = discovery.SuggestAccounts(t.Context(), conn, existing[0], 10, discovery.SuggestOptions{Now: posted.Add(14 * 24 * time.Hour)})
	require.NoError(t, err)
	require.Equal(t, []string{"boundary"}, activityNames(rows))
	require.NoError(t, conn.Model(&models.Status{}).Where("account_id = ?", account.Id).Update("deleted_at", asOf).Error)
	rows, err = discovery.SuggestAccounts(t.Context(), conn, existing[0], 10, discovery.SuggestOptions{Now: posted.Add(14 * 24 * time.Hour)})
	require.NoError(t, err)
	require.Empty(t, rows, "deletion is checked again on read")
}

func TestSuggestionActivityRanksEligibleAndDropsInactiveHighScore(t *testing.T) {
	conn, existing := suggestionFixture(t)
	asOf := time.Now().UTC().Truncate(time.Second)
	_, err := models.SetRecommendationPreference(conn, existing[0].UserId, models.RecommendationPersonalized)
	require.NoError(t, err)
	suggestionUser(t, conn, "inactive-popular", "https://cdn.example/media/inactive-popular.png", asOf)
	suggestionUser(t, conn, "active-quiet", "https://cdn.example/media/active-quiet.png", asOf.Add(-10*24*time.Hour))
	var popular, quiet models.Account
	require.NoError(t, conn.Where("username = ?", "inactive-popular").First(&popular).Error)
	require.NoError(t, conn.Where("username = ?", "active-quiet").First(&quiet).Error)
	require.NoError(t, conn.Where("account_id = ?", popular.Id).Delete(&models.Status{}).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Follow{
		AccountId: existing[4].Id, FollowId: popular.Id, Status: models.FollowStatusFollowed,
	}).Error)
	activityPost(t, conn, quiet.Id, asOf.Add(-2*time.Hour), models.StatusVisibilityPublic, nil)
	activityPost(t, conn, existing[1].Id, asOf.Add(-9*24*time.Hour), models.StatusVisibilityPublic, nil)

	rows, err := discovery.SuggestAccounts(t.Context(), conn, existing[0], 1, discovery.SuggestOptions{Now: asOf})
	require.NoError(t, err)
	require.Equal(t, []string{"active-quiet"}, activityNames(rows), "an inactive high score must not take the only slot")

	activityPost(t, conn, popular.Id, asOf.Add(-10*24*time.Hour), models.StatusVisibilityPublic, nil)
	rows, err = discovery.SuggestAccounts(t.Context(), conn, existing[0], 1, discovery.SuggestOptions{Now: asOf})
	require.NoError(t, err)
	require.Equal(t, []string{"inactive-popular"}, activityNames(rows), "among accounts inside the window the existing score order stands")
}

func TestSuggestionActivityRefillSkipsInactiveAccounts(t *testing.T) {
	conn, existing := suggestionFixture(t)
	require.NoError(t, conn.Session(&gorm.Session{AllowGlobalUpdate: true, SkipHooks: true}).Model(&models.Account{}).Update("avatar", "").Error)
	asOf := time.Now().UTC().Truncate(time.Second)
	seed := strings.Repeat("cd", 32)
	users := make([]*models.User, 400)
	for i := range users {
		name := fmt.Sprintf("filler-%03d", i)
		users[i] = &models.User{UserName: name, EmailHash: name, Status: models.UserStatusNormal}
	}
	require.NoError(t, conn.CreateInBatches(users, 50).Error)
	avatar := "/api/v1/avatars/default/" + seed + ".png?v=1"
	accounts := make([]*models.Account, 400)
	statuses := make([]*models.Status, 400)
	for i, u := range users {
		at := asOf.Add(-time.Duration(i) * time.Second)
		accounts[i] = &models.Account{
			Id: u.Id + 1000, UserId: u.Id, Username: u.UserName, Acct: u.UserName, DisplayName: u.UserName,
			Discoverable: true, Avatar: avatar, LastStatusAt: &at,
		}
		posted := asOf
		statuses[i] = &models.Status{AccountId: accounts[i].Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &posted, UpdatedAt: &posted}
	}
	require.NoError(t, conn.Omit("Extra", "User").CreateInBatches(accounts, 50).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Content").CreateInBatches(statuses, 50).Error)

	decoyAt := asOf.Add(-401 * time.Second)
	suggestionUser(t, conn, "decoy", "https://cdn.example/media/decoy.png", decoyAt)
	var decoy models.Account
	require.NoError(t, conn.Where("username = ?", "decoy").First(&decoy).Error)
	require.NoError(t, conn.Where("account_id = ?", decoy.Id).Delete(&models.Status{}).Error)
	keeperAt := asOf.Add(-402 * time.Second)
	suggestionUser(t, conn, "keeper", "https://cdn.example/media/keeper.png", keeperAt)
	var keeper models.Account
	require.NoError(t, conn.Where("username = ?", "keeper").First(&keeper).Error)
	activityPost(t, conn, keeper.Id, asOf.Add(-time.Hour), models.StatusVisibilityPublic, nil)

	rows, err := discovery.SuggestAccounts(t.Context(), conn, existing[0], 5, discovery.SuggestOptions{Now: asOf})
	require.NoError(t, err)
	require.Equal(t, []string{"keeper"}, activityNames(rows))
}
