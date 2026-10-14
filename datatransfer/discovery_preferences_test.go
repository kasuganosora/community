package datatransfer

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestImportedDiscoveryPreferencesRequireRemoteRevalidation(t *testing.T) {
	conn, bundle, options := personalFixture(t)
	bundle.Accounts[0].Discoverable = true
	bundle.Accounts[0].NoIndex = true
	bundle.Accounts[0].HideCollections = true
	bundle.Accounts[1].Discoverable = true
	bundle.Accounts[1].RemoteDiscoverable = new(true)
	bundle.Accounts[1].RemoteIndexable = new(false)
	_, err := Import(context.Background(), conn, bundle, options, nil)
	require.NoError(t, err)
	var owner, remote models.Account
	require.NoError(t, conn.First(&owner, options.OwnerAccountID).Error)
	require.True(t, owner.Discoverable)
	require.True(t, owner.NoIndex)
	require.True(t, owner.HideCollections)
	require.False(t, owner.AllowsRecommendation(time.Now()))
	require.NoError(t, conn.Where("origin_id = ?", bundle.Accounts[1].ActorURI).First(&remote).Error)
	require.Equal(t, new(true), remote.RemoteDiscoverable)
	require.Equal(t, new(false), remote.RemoteIndexable)
	require.True(t, remote.NoIndex)
	require.Nil(t, remote.RemoteDiscoveryCheckedAt)
	require.False(t, remote.AllowsRecommendation(time.Now()), "archives cannot assert a fresh canonical actor check")
}

func TestPersonalExportIncludesOnlyOwnersRecommendationFeedbackAsArchive(t *testing.T) {
	conn, bundle, options := personalFixture(t)
	require.NoError(t, conn.AutoMigrate(&models.Filter{}, &models.MuteStatus{}, &models.Notification{}, &models.StatusHistory{}))
	rows := []models.RecommendationFeedback{
		{Scope: "user", OwnerID: options.OwnerAccountID, Action: "hide_tag", TargetKind: "tag", TargetKey: "owners-choice"},
		{Scope: "user", OwnerID: options.OwnerAccountID + 1, Action: "hide_tag", TargetKind: "tag", TargetKey: "someone-else"},
		{Scope: "site", OwnerID: 0, Action: "suppress", TargetKind: "tag", TargetKey: "admin-rule"},
	}
	for _, row := range rows {
		require.NoError(t, conn.Create(&row).Error)
	}
	var output bytes.Buffer
	require.NoError(t, ExportPersonal(context.Background(), conn, options.OwnerAccountID, options.TargetBaseURL, &output))
	archive, err := OpenArchive(context.Background(), "backup.zip", bytes.NewReader(output.Bytes()), Limits{})
	require.NoError(t, err)
	defer archive.Close()
	exported, err := ParseArchive(context.Background(), archive, ParseOptions{})
	require.NoError(t, err)
	var feedback []models.RecommendationFeedback
	for _, record := range exported.Records {
		if record.Kind != "recommendation_feedback" {
			continue
		}
		var row models.RecommendationFeedback
		require.NoError(t, json.Unmarshal(record.Data, &row))
		feedback = append(feedback, row)
	}
	require.Len(t, feedback, 1)
	require.Equal(t, "owners-choice", feedback[0].TargetKey)
	// A received archive stays an opaque private record. It must not apply an
	// exclusion to an unrelated target ID in this site's recommendation table.
	bundle.Records = exported.Records
	_, err = Import(context.Background(), conn, bundle, options, nil)
	require.NoError(t, err)
	var count int64
	require.NoError(t, conn.Model(&models.RecommendationFeedback{}).Count(&count).Error)
	require.EqualValues(t, 3, count)
}
