package activitypub

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCatPreferenceSurvivesFederationAndSparseRefresh(t *testing.T) {
	conn := searchFederationDB(t)
	var actor Actor
	require.NoError(t, json.Unmarshal([]byte(`{"id":"https://remote.example/users/cat","type":"Person","preferredUsername":"cat","isCat":true}`), &actor))
	account, err := createRemoteAccount(conn, &actor)
	require.NoError(t, err)
	require.True(t, account.IsCat)
	actor.IsCat = nil
	require.NoError(t, applyActorProfile(conn, &account, &actor))
	require.True(t, account.IsCat, "missing fields must not reset an existing cat preference")
	value := false
	actor.IsCat = &value
	require.NoError(t, applyActorProfile(conn, &account, &actor))
	require.False(t, account.IsCat)
	require.NoError(t, json.Unmarshal([]byte(`{"id":"https://remote.example/users/cat","isCat":"invalid"}`), &actor))
	require.Nil(t, actor.IsCat)
}

func TestLocalActorPublishesCatFlagWithMisskeyNamespace(t *testing.T) {
	conn := outboundDB(t)
	account := outboundAccount(t, conn, "cat_profile", false)
	account.IsCat = true
	actor, err := buildActorProfile(account, nil)
	require.NoError(t, err)
	require.NotNil(t, actor.IsCat)
	require.True(t, *actor.IsCat)
	data, err := json.Marshal(actor)
	require.NoError(t, err)
	require.Contains(t, string(data), `"isCat":true`)
	require.Contains(t, string(data), `"isCat":"misskey:isCat"`)
	require.NotContains(t, string(data), "followed_message")
}
