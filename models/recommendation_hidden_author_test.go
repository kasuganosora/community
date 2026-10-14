package models

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRecommendationHiddenAuthorBeyondFeedbackPage(t *testing.T) {
	conn := recommendationTestDB(t)
	viewer := recommendationTestAccount(t, conn, 1)
	target := recommendationTestAccount(t, conn, 999999)
	duplicate := recommendationTestAccount(t, conn, 2)
	require.NoError(t, conn.Model(duplicate).UpdateColumn("acct", target.Acct).Error)
	rows := make([]RecommendationFeedback, 0, 5001)
	for i := range 5000 {
		rows = append(rows, RecommendationFeedback{Scope: "user", OwnerID: viewer.Id, Action: "hide_author", TargetKind: "account", TargetKey: strconv.Itoa(1000 + i)})
	}
	rows = append(rows, RecommendationFeedback{Scope: "user", OwnerID: viewer.Id, Action: "hide_author", TargetKind: "account", TargetKey: strconv.FormatUint(target.Id, 10)})
	require.NoError(t, conn.CreateInBatches(rows, 250).Error)
	allowed, err := FilterRecommendableAccounts(conn, viewer, []*Account{duplicate}, false)
	require.NoError(t, err)
	require.Empty(t, allowed, "an unrelated feedback volume must not limit the author's exclusion")
}

func TestRecommendationHiddenAuthorKeepsDeletedSourceAndScope(t *testing.T) {
	for _, tc := range []struct {
		name    string
		scope   string
		owner   uint64
		action  string
		expired bool
		blocked bool
	}{
		{"own exclusion", "user", 1, "hide_author", false, true},
		{"site exclusion", "site", 0, "suppress", false, true},
		{"other viewer", "user", 99, "hide_author", false, false},
		{"expired exclusion", "user", 1, "hide_author", true, false},
		{"suggestion dismissal", "user", 1, "dismiss_suggestion", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := recommendationTestDB(t)
			viewer := recommendationTestAccount(t, conn, 1)
			target := recommendationTestAccount(t, conn, 2)
			duplicate := recommendationTestAccount(t, conn, 3)
			require.NoError(t, conn.Model(target).UpdateColumn("acct", "Author@remote.test").Error)
			require.NoError(t, conn.Model(duplicate).UpdateColumn("acct", "  author@REMOTE.test  ").Error)
			row := RecommendationFeedback{Scope: tc.scope, OwnerID: tc.owner, Action: tc.action, TargetKind: "account", TargetKey: strconv.FormatUint(target.Id, 10)}
			if tc.expired {
				expired := time.Now().Add(-time.Minute)
				row.ExpiresAt = &expired
			}
			require.NoError(t, conn.Create(&row).Error)
			require.NoError(t, conn.Delete(target).Error)
			allowed, err := FilterRecommendableAccounts(conn, viewer, []*Account{duplicate}, false)
			require.NoError(t, err)
			if tc.blocked {
				require.Empty(t, allowed, "soft deletion of the hidden row must retain the author's exclusion")
			} else {
				require.Len(t, allowed, 1, "another viewer's, expired or unrelated feedback must not hide the author")
			}
		})
	}
}
