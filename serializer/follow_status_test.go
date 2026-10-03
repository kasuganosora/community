package serializer

import (
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestToFollowStatusDistinguishesPending(t *testing.T) {
	account := &models.Account{Id: 42}
	pending := ToFollowStatus(account, &models.Follow{Status: models.FollowStatusRequested})
	assert.False(t, pending.Following)
	assert.True(t, pending.Requested)
	accepted := ToFollowStatus(account, &models.Follow{Status: models.FollowStatusFollowed})
	assert.True(t, accepted.Following)
	assert.False(t, accepted.Requested)
	removed := ToFollowStatus(account, nil)
	assert.False(t, removed.Following)
	assert.False(t, removed.Requested)
}
