package serializer

import (
	"encoding/json"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestDeletingAccountNeverExposesPendingProfileData(t *testing.T) {
	now := time.Now()
	a := &models.Account{Id: 9, Username: "alice", DisplayName: "private name", Note: "private bio", Avatar: "https://example.test/private.png", Header: "https://example.test/private-banner.png", DeletionRequestedAt: &now, Discoverable: true, Extra: &models.AccountExtra{FollowersCount: 12}}
	result := ToAccount(a)
	require.Equal(t, "alice", result.Username)
	require.Empty(t, result.Note)
	require.Empty(t, result.DisplayName)
	require.Zero(t, result.FollowersCount)
	require.True(t, result.NoIndex)
	require.False(t, result.Discoverable)
	body, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(body), "private")
	require.Equal(t, "private bio", a.Note, "serialization must not mutate worker metadata")
}
