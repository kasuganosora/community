package models

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func feedMetadataBoundary(t *testing.T) FeedEventMetadata {
	t.Helper()
	m := FeedEventMetadata{Reason: "x", RecipientIDs: []uint64{2, 3}}
	base, err := json.Marshal(m)
	require.NoError(t, err)
	m.Reason = strings.Repeat("x", MaxFeedMetadataBytes-len(base)+1)
	return m
}

func TestFeedMetadataEncodedBoundary(t *testing.T) {
	boundary := feedMetadataBoundary(t)
	encoded, err := json.Marshal(boundary)
	require.NoError(t, err)
	require.Len(t, encoded, MaxFeedMetadataBytes)
	var roundtrip FeedEventMetadata
	require.NoError(t, json.Unmarshal(encoded, &roundtrip))
	require.Equal(t, boundary, roundtrip)
	boundary.Reason += "x"
	_, err = json.Marshal(boundary)
	require.ErrorIs(t, err, ErrFeedMetadataTooLarge)
	require.NotContains(t, err.Error(), boundary.Reason[:100])
	// Counting runes or unescaped input would admit both of these incorrectly.
	for _, reason := range []string{strings.Repeat("界", MaxFeedMetadataBytes/3+1), strings.Repeat("<", MaxFeedMetadataBytes/6+1)} {
		_, err = json.Marshal(FeedEventMetadata{Reason: reason})
		require.ErrorIs(t, err, ErrFeedMetadataTooLarge)
	}
}

func TestFeedMetadataPersistenceAndBusinessRollback(t *testing.T) {
	conn := feedTestDB(t)
	require.NoError(t, conn.Migrator().DropTable("feed_metadata_business"))
	require.NoError(t, conn.Exec("CREATE TABLE feed_metadata_business (id BIGINT PRIMARY KEY, value BIGINT)").Error)
	require.NoError(t, conn.Exec("INSERT INTO feed_metadata_business VALUES (1,0)").Error)
	boundary := feedMetadataBoundary(t)
	event := FeedEvent{EventKey: "boundary", Kind: FeedInvalidate, ObjectType: "account", ObjectID: 1, ActorID: 1, Metadata: boundary}
	require.NoError(t, conn.Create(&event).Error)
	var loaded FeedEvent
	require.NoError(t, conn.First(&loaded, event.ID).Error)
	require.Equal(t, boundary, loaded.Metadata)
	oversized := boundary
	oversized.Reason += "x"
	// The real queue entrypoint participates in its caller's transaction.
	err := conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("UPDATE feed_metadata_business SET value=1 WHERE id=1").Error; err != nil {
			return err
		}
		return QueueFeedInvalidation(tx, oversized)
	})
	require.ErrorIs(t, err, ErrFeedMetadataTooLarge)
	var value int
	require.NoError(t, conn.Raw("SELECT value FROM feed_metadata_business WHERE id=1").Scan(&value).Error)
	require.Zero(t, value)
	var count int64
	require.NoError(t, conn.Model(&FeedEvent{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	event.Metadata = oversized
	require.ErrorIs(t, conn.Save(&event).Error, ErrFeedMetadataTooLarge)
	require.ErrorIs(t, conn.Model(&FeedEvent{}).Where("id = ?", event.ID).Updates(FeedEvent{Metadata: oversized}).Error, ErrFeedMetadataTooLarge)
	require.NoError(t, conn.First(&loaded, event.ID).Error)
	require.Equal(t, boundary, loaded.Metadata)
	// Lease updates remain available without rewriting or dropping the metadata.
	require.NoError(t, conn.Model(&FeedEvent{}).Where("id = ?", event.ID).UpdateColumn("last_error_code", "dependency_timeout").Error)
	require.NoError(t, conn.First(&loaded, event.ID).Error)
	require.Equal(t, boundary, loaded.Metadata)
	require.Equal(t, "dependency_timeout", loaded.LastErrorCode)
}
