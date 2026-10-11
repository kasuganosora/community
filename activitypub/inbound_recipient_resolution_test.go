package activitypub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUnknownDirectRecipientsAreExplicitBoundedAndRetryable(t *testing.T) {
	conn := persistenceDB(t, false)
	first := "https://one.example/users/one"
	second := "https://two.example/users/two"
	obj := map[string]any{"to": []string{first, first, second}, "content": "@outsider", "tag": []any{map[string]any{"type": "Mention", "href": "https://outsider.example/users/outsider"}}}
	fail := true
	failStatus := false
	require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("recipient-status-failure", func(tx *gorm.DB) {
		if failStatus && tx.Statement.Table == "statuses" {
			tx.AddError(errors.New("injected status write failure"))
		}
	}))
	defer conn.Callback().Create().Remove("recipient-status-failure")
	calls := []string{}
	resolve := func(ctx context.Context, uri string) (*models.Account, error) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 20*time.Second)
		calls = append(calls, uri)
		if uri == second && fail {
			return nil, errors.New("transient upstream failure")
		}
		a, err := createRemoteAccount(activityDB(ctx), &Actor{ID: uri, Type: "Person", PreferredUsername: "peer"})
		return &a, err
	}
	invoke := func() int {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		processInboxTransaction(context.Background(), c, "https://sender.example/activity/recipients", func(ctx context.Context) {
			accounts, err := recipientAccountsWithResolver(ctx, obj, false, resolve)
			if err != nil {
				c.JSON(500, gin.H{"error": "recipient unavailable"})
				return
			}
			require.Len(t, accounts, 2)
			uri := "https://sender.example/notes/recipient-resolution"
			status := models.Status{AccountId: accounts[0].Id, OriginUri: &uri, Visibility: models.StatusVisibilityDirect, Content: models.StatusContent{Content: "addressed private note"}, ToAccounts: accounts}
			if err := persistIncomingStatus(ctx, activityDB(ctx), &status); err != nil {
				c.JSON(500, gin.H{"error": "status unavailable"})
				return
			}
			c.Status(200)
		})
		return w.Code
	}
	require.Equal(t, 500, invoke())
	var count int64
	require.NoError(t, conn.Model(&models.Account{}).Count(&count).Error)
	require.Zero(t, count, "partial identity import rolls back with inbox failure")
	require.NoError(t, conn.Model(&ProcessedActivity{}).Count(&count).Error)
	require.Zero(t, count)
	fail = false
	failStatus = true
	require.Equal(t, 500, invoke())
	require.NoError(t, conn.Model(&models.Account{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, conn.Model(&models.Status{}).Count(&count).Error)
	require.Zero(t, count)
	failStatus = false
	require.Equal(t, 200, invoke())
	require.Equal(t, []string{first, second, first, second, first, second}, calls)
	require.Equal(t, 200, invoke())
	require.Len(t, calls, 6, "deduped activity does not refetch")
}
func TestDirectResolutionDoesNotFetchPublicTagsCollectionsOrKnownGroups(t *testing.T) {
	conn := persistenceDB(t, false)
	addresses := []string{}
	for i := 0; i < 40; i++ {
		uri := fmt.Sprintf("https://remote.example/users/%d", i)
		domain := "remote.example"
		require.NoError(t, conn.Create(&models.Account{Username: fmt.Sprint(i), Domain: &domain, OriginId: uri}).Error)
		addresses = append(addresses, uri)
	}
	calls := 0
	resolve := func(context.Context, string) (*models.Account, error) {
		calls++
		return nil, errors.New("must not fetch")
	}
	accounts, err := recipientAccountsWithResolver(context.Background(), map[string]any{"to": addresses}, false, resolve)
	require.NoError(t, err)
	require.Len(t, accounts, 40)
	require.Zero(t, calls)
	for _, obj := range []map[string]any{
		{"to": []string{"Public", "https://unknown.example/users/peer"}},
		{"to": []string{"https://remote.example/users/0"}, "cc": []string{"https://unknown.example/users/peer/following"}, "tag": []any{map[string]any{"type": "Mention", "href": "https://unknown.example/users/peer"}}},
		{"to": []string{"https://unknown.example/users/owner/followers", "https://unknown.example/users/peer"}},
	} {
		_, err := recipientAccountsWithResolver(context.Background(), obj, false, resolve)
		require.NoError(t, err)
	}
	require.Zero(t, calls)
}
func TestDirectResolutionRejectsOverflowAndPrivateNetwork(t *testing.T) {
	t.Setenv("APP_BASE_URL", "https://community.example")
	persistenceDB(t, false)
	addresses := []string{}
	for i := 0; i < 33; i++ {
		addresses = append(addresses, fmt.Sprintf("https://remote.example/users/%d", i))
	}
	calls := 0
	resolve := func(ctx context.Context, uri string) (*models.Account, error) {
		calls++
		a, err := createRemoteAccount(activityDB(ctx), &Actor{ID: uri, Type: "Person", PreferredUsername: fmt.Sprint(calls)})
		return &a, err
	}
	_, err := recipientAccountsWithResolver(context.Background(), map[string]any{"to": addresses}, false, resolve)
	require.ErrorContains(t, err, "too many unresolved")
	require.Equal(t, 32, calls)
	_, err = recipientAccountsWithResolver(context.Background(), map[string]any{"to": []string{"http://127.0.0.1/users/private"}}, false, resolveDirectRecipient)
	require.Error(t, err)
	require.Equal(t, 32, calls)
}

func TestDirectRecipientFetchRejectsCollectionsAndMismatchedIdentity(t *testing.T) {
	conn := persistenceDB(t, false)
	old := sharedHTTPClient
	defer func() { sharedHTTPClient = old }()
	for _, body := range []string{
		`{"id":"https://remote.example/users/target","type":"OrderedCollection","inbox":"https://remote.example/inbox"}`,
		`{"id":"https://other.example/users/target","type":"Person","inbox":"https://remote.example/inbox"}`,
	} {
		sharedHTTPClient = &http.Client{Transport: outboundTransport(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		_, err := recipientAccounts(context.Background(), map[string]any{"to": []string{"https://remote.example/users/target"}}, false)
		require.Error(t, err)
	}
	var count int64
	require.NoError(t, conn.Model(&models.Account{}).Count(&count).Error)
	require.Zero(t, count)
}
