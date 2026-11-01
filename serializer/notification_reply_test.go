package serializer

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestMentionNotificationIncludesReplyParent(t *testing.T) {
	conn := timelineBatchDB(t)
	require.NoError(t, conn.AutoMigrate(&models.AccountExtra{}, &models.Notification{}, &models.MediaAttachment{}, &models.Application{}, &models.Poll{}, &models.PollOption{}, &models.Tag{}, &models.Emoji{}, &models.StatusEmoji{}))
	author := &models.Account{Id: 20, Username: "luna", DisplayName: "Luna"}
	replier := &models.Account{Id: 21, Username: "skyone", DisplayName: "Skyone"}
	require.NoError(t, conn.Create(author).Error)
	require.NoError(t, conn.Create(replier).Error)
	parent := batchStatus(200, author, models.StatusVisibilityPublic)
	parent.Content.StatusId = parent.Id
	parent.Content.Content = "original post"
	require.NoError(t, conn.Omit("Account", "ToAccounts").Create(parent).Error)
	child := batchStatus(201, replier, models.StatusVisibilityPublic)
	child.InReplyToId = &parent.Id
	child.InReplyToAccountId = &author.Id
	child.Content.StatusId = child.Id
	child.Content.Content = "reply body"
	require.NoError(t, conn.Omit("Account", "ToAccounts").Create(child).Error)

	out, err := ToNotifications([]*models.Notification{{
		Id: 9, AccountId: author.Id, FromAccountId: replier.Id, StatusId: child.Id, Type: models.NotificationTypeMention,
	}}, author)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.NotNil(t, out[0].Status)
	require.NotNil(t, out[0].Status.InReplyToId)
	require.Equal(t, "200", *out[0].Status.InReplyToId)
	require.NotNil(t, out[0].Status.Reply)
	require.Equal(t, "200", out[0].Status.Reply.Id)
	require.Contains(t, out[0].Status.Reply.Content, "original post")
	require.NotNil(t, out[0].Status.Reply.Account)
	require.Equal(t, "luna", out[0].Status.Reply.Account.Username)
}
