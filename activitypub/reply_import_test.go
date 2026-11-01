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

func TestImportedReplyLinksAKnownParent(t *testing.T) {
	conn := sensitiveDB(t)
	now := time.Now()
	author := &models.Account{Username: "luna", DisplayName: "Luna", CreatedAt: &now, UpdatedAt: &now}
	require.NoError(t, conn.Omit("Extra").Create(author).Error)
	parent := &models.Status{
		AccountId: author.Id, Visibility: models.StatusVisibilityPublic,
		CreatedAt: &now, UpdatedAt: &now,
		Content: models.StatusContent{Content: "original post"},
	}
	require.NoError(t, conn.Create(parent).Error)
	_, err := createRemoteAccount(conn, &Actor{ID: "https://remote.example/users/sky", PreferredUsername: "sky"})
	require.NoError(t, err)
	parentURI := systemsettings.GetBaseURL() + "/users/luna/statuses/" + strconv.FormatUint(parent.Id, 10)
	ctx := context.WithValue(context.Background(), inboxTransactionKey{}, conn)
	uri := "https://remote.example/notes/reply-parent"
	note := noteObject(uri, map[string]any{
		"attributedTo": "https://remote.example/users/sky",
		"content":      "<p>reply body</p>",
		"inReplyTo":    parentURI,
	})
	saved, err := importFetchedStatus(ctx, uri, note)
	require.NoError(t, err)
	var row models.Status
	require.NoError(t, conn.First(&row, saved.Id).Error)
	require.NotNil(t, row.InReplyToId)
	require.Equal(t, parent.Id, *row.InReplyToId)
	require.NotNil(t, row.InReplyToAccountId)
	require.Equal(t, author.Id, *row.InReplyToAccountId)

	// A reply saved before its parent existed can be repaired on a later delivery.
	unlinked := &models.Status{
		AccountId: row.AccountId, Visibility: models.StatusVisibilityPublic,
		OriginUri: strPtr("https://remote.example/notes/late"), CreatedAt: &now, UpdatedAt: &now,
		Content: models.StatusContent{Content: "late reply"},
	}
	require.NoError(t, conn.Create(unlinked).Error)
	require.NoError(t, backfillReplyParent(ctx, unlinked, parentURI))
	var repaired models.Status
	require.NoError(t, conn.First(&repaired, unlinked.Id).Error)
	require.NotNil(t, repaired.InReplyToId)
	require.Equal(t, parent.Id, *repaired.InReplyToId)
}

func strPtr(v string) *string { return &v }
