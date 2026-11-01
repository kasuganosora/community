package activitypub

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func TestRecoverReplyParentUsesOriginAndDoesNotFetchParent(t *testing.T) {
	conn := sensitiveDB(t)
	now := time.Now()
	author := &models.Account{Username: "luna", DisplayName: "Luna", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(author).Error)
	parent := &models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: models.StatusContent{Content: "original post"}}
	require.NoError(t, conn.Create(parent).Error)
	origin := "https://remote.example/notes/reply-1"
	domain := "remote.example"
	reply := &models.Status{AccountId: author.Id, Domain: &domain, OriginUri: &origin, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now, Content: models.StatusContent{Content: "reply body"}}
	require.NoError(t, conn.Create(reply).Error)
	parentURI := systemsettings.GetBaseURL() + "/users/luna/statuses/" + strconv.FormatUint(parent.Id, 10)
	asked := []string{}
	linked, err := RecoverUnlinkedReplyParents(context.Background(), 10, func(_ context.Context, uri string) (map[string]any, error) {
		asked = append(asked, uri)
		if uri != origin {
			return nil, errNotLocalPoll
		}
		return map[string]any{"id": origin, "type": "Note", "inReplyTo": map[string]any{"type": "Note", "id": parentURI}}, nil
	})
	require.NoError(t, err)
	require.Equal(t, []string{origin}, asked)
	require.GreaterOrEqual(t, linked, 1)
	var saved models.Status
	require.NoError(t, conn.First(&saved, reply.Id).Error)
	require.NotNil(t, saved.InReplyToId)
	require.Equal(t, parent.Id, *saved.InReplyToId)
	require.NotNil(t, saved.InReplyToAccountId)
	require.Equal(t, author.Id, *saved.InReplyToAccountId)
}
