package status

import (
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReblog_RejectsDirectAndForeignPrivateStatuses(t *testing.T) {
	ts := newTestSetup(t)

	authorUser := ts.fix.CreateUser(func(u *models.User) { u.UserName = "privauthor" })
	authorAcct := ts.fix.CreateAccount(authorUser, func(a *models.Account) { a.Username = "privauthor" })

	// Viewer follows authorAcct, so viewer CAN see authorAcct's private status,
	// but still must NOT be allowed to reblog it publicly.
	require.NoError(t, ts.db.Create(&models.Follow{
		AccountId: ts.acct.Id,
		FollowId:  authorAcct.Id,
		Status:    models.FollowStatusFollowed,
	}).Error)

	privateStatus := ts.fix.CreateStatus(authorAcct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityPrivate
	})

	cPriv, wPriv := newGinContextPost(t, gin.Params{{Key: "id", Value: strconv.FormatUint(privateStatus.Id, 10)}}, ReblogRequest{
		Visibility: "public",
	})
	setAccount(cPriv, ts.acct)
	safeHandler(cPriv, Reblog)
	assert.Equal(t, 422, wPriv.Code)

	// Direct message addressed to ts.acct: visible to ts.acct, but must NOT be rebloggable.
	directStatus := ts.fix.CreateStatus(authorAcct, func(s *models.Status) {
		s.Visibility = models.StatusVisibilityDirect
	})
	require.NoError(t, ts.db.Create(&models.StatusToAccount{
		StatusId:  directStatus.Id,
		AccountId: ts.acct.Id,
	}).Error)

	cDirect, wDirect := newGinContextPost(t, gin.Params{{Key: "id", Value: strconv.FormatUint(directStatus.Id, 10)}}, ReblogRequest{
		Visibility: "public",
	})
	setAccount(cDirect, ts.acct)
	safeHandler(cDirect, Reblog)
	assert.Equal(t, 422, wDirect.Code)

	// Status.CanView must also reject a public reblog wrapper around an unauthorized private/direct target.
	reblogWrapper := &models.Status{
		Id:         9999,
		AccountId:  authorAcct.Id,
		Visibility: models.StatusVisibilityPublic,
		ReblogId:   &directStatus.Id,
		Reblog:     directStatus,
	}
	stranger := &models.Account{Id: 7777}
	assert.False(t, reblogWrapper.CanView(stranger, nil), "CanView must enforce target Reblog visibility")
}
