package serializer

import (
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRemoteAccountPreservesDiscoveredActorURI(t *testing.T) {
	domain := "misskey.example"
	remote := &models.Account{Username: "alice", Domain: &domain, OriginId: "https://misskey.example/users/opaque-id"}
	account := ToAccount(remote)
	require.Equal(t, "alice@misskey.example", account.Acct)
	require.Equal(t, remote.OriginId, account.Uri)
	// A legacy non-URI remote ID must not become a broken or unsafe URI.
	remote.OriginId = "javascript:alert(1)"
	require.Equal(t, "https://misskey.example/users/alice", ToAccount(remote).Uri)
}
