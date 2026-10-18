package streaming

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestVisitorCannotSeeSigninOnlyStatuses(t *testing.T) {
	hidden := &models.Account{Id: 1, RequireSigninToView: true}
	open := &models.Account{Id: 2}
	original := &models.Status{Id: 10, AccountId: 1, Account: hidden}
	require.False(t, visitorCanSeeStatuses(models.Statuses{original}))
	require.False(t, visitorCanSeeStatuses(models.Statuses{{Id: 11, AccountId: 2, Account: open, Reblog: original}}), "boost of a sign-in-only post")
	require.True(t, visitorCanSeeStatuses(models.Statuses{{Id: 12, AccountId: 2, Account: open}}))
}
