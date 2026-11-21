package serializer

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestRecommendationQuoteKeepsWarnContextAndViewerMetadata(t *testing.T) {
	conn := timelineBatchDB(t)
	author := &models.Account{Id: 1, Username: "author"}
	quoter := &models.Account{Id: 2, Username: "quoter"}
	viewer := &models.Account{Id: 3, Username: "viewer"}
	quoted := batchStatus(10, author, models.StatusVisibilityPublic)
	quoted.Content.Content = "needle"
	root := batchStatus(20, quoter, models.StatusVisibilityPublic)
	root.QuoteId, root.Quote = &quoted.Id, quoted
	require.NoError(t, conn.Create(&models.FavoriteStatus{AccountId: viewer.Id, StatusId: quoted.Id}).Error)
	require.NoError(t, conn.Create(&models.Filter{AccountId: viewer.Id, Title: "home only", Context: []string{"home"}, FilterAction: models.FilterActionWarn, Keywords: []*models.FilterKeyword{{Keyword: "needle"}}}).Error)
	for _, contextName := range []string{"home", "public"} {
		out, err := ToStatusesContext(models.WithFilterContext(context.Background(), contextName), models.Statuses{root}, viewer)
		require.NoError(t, err)
		require.Len(t, out, 1)
		require.NotNil(t, out[0].Quote)
		require.Equal(t, "automatic", out[0].QuoteApproval.CurrentUser)
		require.Equal(t, "accepted", out[0].Quote.State)
		quote := out[0].Quote.QuotedStatus
		require.NotNil(t, quote)
		require.True(t, quote.Favourited)
		require.Empty(t, out[0].Filtered)
		if contextName == "home" {
			require.Len(t, quote.Filtered, 1)
			require.Equal(t, "warn", quote.Filtered[0].Filter.FilterAction)
		} else {
			require.Empty(t, quote.Filtered)
		}
	}
	anonymous, err := ToStatusesContext(context.Background(), models.Statuses{root}, nil)
	require.NoError(t, err)
	require.Len(t, anonymous, 1)
	require.Equal(t, []string{"public"}, anonymous[0].QuoteApproval.Automatic)
	require.Equal(t, "denied", anonymous[0].QuoteApproval.CurrentUser)
	single, err := ToStatus(root, viewer)
	require.NoError(t, err)
	require.NotNil(t, single.Quote.QuotedStatus)
	require.True(t, single.Quote.QuotedStatus.Favourited)
	quoted.Visibility = models.StatusVisibilityPrivate
	out, err := ToStatusesContext(models.WithFilterContext(context.Background(), "home"), models.Statuses{root}, viewer)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.Equal(t, &StatusQuote{State: "unauthorized"}, out[0].Quote)
	single, err = ToStatus(root, viewer)
	require.NoError(t, err)
	require.Equal(t, out[0].Quote, single.Quote)
	require.NoError(t, conn.Create(&models.Follow{AccountId: viewer.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	out, err = ToStatusesContext(models.WithFilterContext(context.Background(), "home"), models.Statuses{root}, viewer)
	require.NoError(t, err)
	require.NotNil(t, out[0].Quote, "accepted follows keep the existing quote visibility semantics")
}
