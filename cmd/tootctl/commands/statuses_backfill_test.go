package commands

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestRepairReplyParentsHelpExplainsCursor(t *testing.T) {
	out, err := runStatuses("repair-reply-parents", "-h")
	require.NoError(t, err)
	require.Contains(t, out, "--after-id")
	require.Contains(t, out, "--limit")
	require.Contains(t, out, "--max-pages")
	require.Contains(t, out, "older than the oldest id")
	require.Contains(t, out, "Do not raise --limit")
	require.Contains(t, out, "--after-id set to the printed oldest id")
}

func TestCheckStatusContentsDistinguishesReblogsAndRepairsMissing(t *testing.T) {
	conn := setupAccountsTest(t)
	require.NoError(t, conn.AutoMigrate(&models.Status{}, &models.StatusContent{}))

	remoteAccount(t, conn, 21, "alice", "remote.example", false)
	require.NoError(t, conn.Create(&models.Account{Id: 22, Username: "localuser"}).Error)

	origID := uint64(500)
	origURI := "https://remote.example/notes/500"
	orig := models.Status{Id: origID, AccountId: 21, OriginUri: &origURI}
	require.NoError(t, conn.Omit("Content").Create(&orig).Error)
	require.NoError(t, conn.Create(&models.StatusContent{StatusId: origID, ContentFormat: "html", Content: "<p>hi</p>"}).Error)

	missingRemoteURI := "https://remote.example/notes/501"
	missingRemote := models.Status{Id: 501, AccountId: 21, OriginUri: &missingRemoteURI}
	require.NoError(t, conn.Omit("Content").Create(&missingRemote).Error)

	missingLocal := models.Status{Id: 502, AccountId: 22}
	require.NoError(t, conn.Omit("Content").Create(&missingLocal).Error)

	reblog := models.Status{Id: 503, AccountId: 22, ReblogId: &origID}
	require.NoError(t, conn.Omit("Content").Create(&reblog).Error)

	out, err := runStatuses("check-contents")
	require.NoError(t, err)
	require.Contains(t, out, "Non-reblog statuses missing status_contents: 2 (local=1, remote=1)")
	require.Contains(t, out, "Reblog statuses without status_contents (expected): 1")

	out, err = runStatuses("check-contents", "--repair-missing")
	require.NoError(t, err)
	require.Contains(t, out, "Backfilled 2 missing non-reblog status_contents row(s) with explicit parent status_id")

	var reblogContentCount int64
	require.NoError(t, conn.Model(&models.StatusContent{}).Where("status_id = ?", reblog.Id).Count(&reblogContentCount).Error)
	require.Zero(t, reblogContentCount, "reblog must not receive a synthetic status_contents row")

	out, err = runStatuses("check-contents")
	require.NoError(t, err)
	require.Contains(t, out, "Non-reblog statuses missing status_contents: 0 (local=0, remote=0)")
}
