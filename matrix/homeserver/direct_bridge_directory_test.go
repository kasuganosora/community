package homeserver

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDirectoryExactRemoteHandleEnablesFirstDMWithoutLeakingPrivateUsers(t *testing.T) {
	hs, conn := directBridgeDB(t)
	search := NewSearchService(hs)
	for _, query := range []string{"remote@remote.example", "@remote@remote.example", "@_ap_remote_remote_example=1:test.local"} {
		result, err := search.SearchUserDirectory(query, 10, "@alice:test.local")
		require.NoError(t, err)
		require.Len(t, result.Results, 1)
		require.Equal(t, "@_ap_remote_remote_example=1:test.local", result.Results[0].UserID)
		profile, ok := hs.bridgeGhostProfile(context.Background(), result.Results[0].UserID)
		require.True(t, ok, "a first-DM target must have a public profile before any room exists")
		require.Equal(t, "remote@remote.example", profile["displayname"])
	}
	for _, query := range []string{"alice", "bob", "remote", "nobody@remote.example"} {
		result, err := search.SearchUserDirectory(query, 10, "@alice:test.local")
		require.NoError(t, err)
		require.Empty(t, result.Results)
	}
	avatar := "mxc://test.local/real-avatar"
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: "!private:test.local", UserID: "@_ap_remote_remote_example=1:test.local", Membership: MembershipJoin, AvatarURL: &avatar}).Error)
	withAvatar, err := search.SearchUserDirectory("remote@remote.example", 10, "@alice:test.local")
	require.NoError(t, err)
	require.Equal(t, avatar, withAvatar.Results[0].AvatarURL)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.Block{AccountId: 2, TargetAccountId: 1}).Error)
	result, err := search.SearchUserDirectory("remote@remote.example", 10, "@alice:test.local")
	require.NoError(t, err)
	require.Empty(t, result.Results)
}

func TestDirectoryDoesNotConfuseRemoteUsersWithLocalAccounts(t *testing.T) {
	hs, conn := directBridgeDB(t)
	search := NewSearchService(hs)
	require.NoError(t, conn.Create(&MatrixRoom{RoomID: "!public:test.local", JoinRule: "public"}).Error)
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: "!public:test.local", UserID: "@outsider:remote.example", Membership: MembershipJoin}).Error)
	result, err := search.SearchUserDirectory("outsider", 10, "@alice:test.local")
	require.NoError(t, err)
	require.Empty(t, result.Results, "a foreign Matrix member must not expose a same-name private local user")
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.Account{}).Where("id = ?", 1).UpdateColumn("username", "alice").Error)
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: "!public:test.local", UserID: "@alice:test.local", Membership: MembershipJoin}).Error)
	result, err = search.SearchUserDirectory("alice", 10, "@alice:test.local")
	require.NoError(t, err)
	require.Len(t, result.Results, 1, "remote accounts must not be presented as local Matrix users")
	require.Equal(t, "@alice:test.local", result.Results[0].UserID)
}
