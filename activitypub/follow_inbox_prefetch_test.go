package activitypub

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

// The Accept must use the inbox resolved before the transaction, never fetch
// inside it (the actor host here does not resolve).
func TestQueueAcceptUsesPrefetchedInbox(t *testing.T) {
	conn := persistenceDB(t, false)
	require.NoError(t, conn.AutoMigrate(&Delivery{}))
	domain := "nowhere.invalid"
	remote := &models.Account{Username: "f", Domain: &domain, OriginId: "https://nowhere.invalid/users/f"}
	local := &models.Account{Username: "local"}
	ctx := context.WithValue(context.Background(), followerInboxKey{}, followerInbox{actor: remote.OriginId, url: "https://nowhere.invalid/users/f/inbox"})
	require.NoError(t, queueAcceptActivity(ctx, local, remote, "https://nowhere.invalid/follow/1"))
	var d Delivery
	require.NoError(t, conn.First(&d).Error)
	require.Equal(t, "https://nowhere.invalid/users/f/inbox", d.TargetInbox)
}
