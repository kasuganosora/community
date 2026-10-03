package status

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

func TestDirectMentionsExcludeURLsHTMLAttributesAndCode(t *testing.T) {
	text := "mail alice@elsewhere.test https://example.test/@urlperson <a href='@attribute@remote.test'>link</a> <code>@htmlcode</code> `@inline` ```\n@fenced\n``` &#64;entity @本地 @bob@Remote.Example, @bob@remote.example"
	mentions, err := directMentions(text)
	require.NoError(t, err)
	require.Equal(t, []directMention{{username: "本地"}, {username: "bob", domain: "remote.example"}}, mentions)
	for _, text := range []string{"@bob@", "@bob@bad.test/path", "@bob@@bad.test"} {
		_, err := directMentions(text)
		require.Error(t, err, text)
	}
}

func TestDirectRecipientsLocalAndValidatedReplyAreDeduplicated(t *testing.T) {
	_, fix, owner := replacementDB(t)
	recipient := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "otheruser" }), func(a *models.Account) { a.Username = "recipient" })
	never := func(context.Context, string) (*models.Account, error) {
		t.Fatal("local account must not resolve over network")
		return nil, nil
	}
	ids, err := prepareDirectRecipientsWithResolver(context.Background(), "@recipient @recipient@"+systemsettings.GetDomain()+" @"+owner.Username, owner.Id, &recipient.Id, never)
	require.NoError(t, err)
	require.Equal(t, []uint64{recipient.Id}, ids)
	ids, err = prepareDirectRecipientsWithResolver(context.Background(), "reply without repeated handle", owner.Id, &recipient.Id, never)
	require.NoError(t, err)
	require.Equal(t, []uint64{recipient.Id}, ids)
	_, err = prepareDirectRecipientsWithResolver(context.Background(), "@"+owner.Username, owner.Id, nil, never)
	require.ErrorContains(t, err, "at least one recipient")
}

func TestDirectRecipientsResolveNewExactAcctBeforeWriteTransaction(t *testing.T) {
	conn, _, owner := replacementDB(t)
	calls := 0
	resolve := func(ctx context.Context, acct string) (*models.Account, error) {
		calls++
		require.Equal(t, "newperson@remote.test", acct)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 15*time.Second)
		// This fixture has a single DB connection. A network resolver running
		// inside an already-open status transaction would block this DB operation.
		var count int64
		require.NoError(t, conn.WithContext(ctx).Model(&models.Account{}).Count(&count).Error)
		domain := "remote.test"
		account := &models.Account{Username: "newperson", Domain: &domain, OriginId: "https://remote.test/users/newperson"}
		require.NoError(t, conn.WithContext(ctx).Create(account).Error)
		return account, nil
	}
	ids, err := prepareDirectRecipientsWithResolver(context.Background(), "hello @newperson@remote.test", owner.Id, nil, resolve)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	require.Equal(t, 1, calls)
	again, err := prepareDirectRecipientsWithResolver(context.Background(), "@newperson@remote.test", owner.Id, nil, resolve)
	require.NoError(t, err)
	require.Equal(t, ids, again)
	require.Equal(t, 1, calls, "known accounts stay local")
	var count int64
	require.NoError(t, conn.Model(&models.Status{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestDirectRecipientsFailClosedAndBoundDiscovery(t *testing.T) {
	_, fix, owner := replacementDB(t)
	fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "otheruser" }), func(a *models.Account) { a.Username = "good" })
	calls := 0
	failed := func(context.Context, string) (*models.Account, error) {
		calls++
		return nil, errors.New("network unavailable")
	}
	ids, err := prepareDirectRecipientsWithResolver(context.Background(), "@good @missing@remote.test", owner.Id, nil, failed)
	require.ErrorContains(t, err, "@missing@remote.test")
	require.Nil(t, ids)
	require.Equal(t, 1, calls)
	calls = 0
	mentions := []string{}
	for i := 0; i <= maxDirectRecipients; i++ {
		mentions = append(mentions, fmt.Sprintf("@person%d@remote.test", i))
	}
	_, err = prepareDirectRecipientsWithResolver(context.Background(), strings.Join(mentions, " "), owner.Id, nil, failed)
	require.ErrorContains(t, err, "at most")
	require.Zero(t, calls)
	_, err = prepareDirectRecipientsWithResolver(context.Background(), "@missinglocal", owner.Id, nil, failed)
	require.ErrorContains(t, err, "@missinglocal")
	require.Zero(t, calls)
	_, err = prepareDirectRecipientsWithResolver(context.Background(), "<img data-user='@good'> `@good`", owner.Id, nil, failed)
	require.ErrorContains(t, err, "at least one recipient")
	_, err = prepareDirectRecipients(context.Background(), "@private@127.0.0.1", owner.Id, nil)
	require.ErrorContains(t, err, "Cannot resolve direct recipient")
}

func TestDirectRecipientsRejectEitherBlockDirectionIncludingReplies(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	recipient := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "blockedrecipient" }), func(a *models.Account) { a.Username = "blockedrecipient" })
	never := func(context.Context, string) (*models.Account, error) {
		t.Fatal("no network expected")
		return nil, nil
	}
	for _, block := range []*models.Block{{AccountId: owner.Id, TargetAccountId: recipient.Id}, {AccountId: recipient.Id, TargetAccountId: owner.Id}} {
		require.NoError(t, conn.Create(block).Error)
		_, err := prepareDirectRecipientsWithResolver(context.Background(), "@blockedrecipient", owner.Id, nil, never)
		require.Error(t, err)
		_, err = prepareDirectRecipientsWithResolver(context.Background(), "reply text", owner.Id, &recipient.Id, never)
		require.Error(t, err)
		require.NoError(t, conn.Delete(block).Error)
	}
}

func callDirectCreate(t *testing.T, owner *models.Account, body any) *httptest.ResponseRecorder {
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/v1/statuses", bytes.NewReader(raw))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("account", owner)
	safeHandler(c, Create)
	return w
}

func TestDirectCreateStructuredRecipientsAndFailedReplacementPreserveOriginal(t *testing.T) {
	conn, fix, owner := replacementDB(t)
	recipient := fix.CreateAccount(fix.CreateUser(func(u *models.User) { u.UserName = "dmrecipient" }), func(a *models.Account) { a.Username = "dmrecipient" })
	w := callDirectCreate(t, owner, map[string]any{"status": "hello @dmrecipient <a data-recipient='@phantom'>link</a> `@phantom`", "visibility": "direct"})
	require.Equal(t, 200, w.Code, w.Body.String())
	originalID := decodedReplacementID(t, w)
	var recipients []models.StatusToAccount
	require.NoError(t, conn.Where("status_id = ?", originalID).Find(&recipients).Error)
	require.Len(t, recipients, 1)
	require.Equal(t, recipient.Id, recipients[0].AccountId)
	for _, content := range []string{"no recipients", "@dmrecipient @missing@127.0.0.1", "`@dmrecipient`"} {
		w = callDirectCreate(t, owner, map[string]any{"status": content, "visibility": "direct"})
		require.Equal(t, 422, w.Code, w.Body.String())
		w = callReplacement(t, owner, originalID, map[string]any{"status": content})
		require.Equal(t, 422, w.Code, w.Body.String())
		require.NoError(t, conn.First(&models.Status{}, originalID).Error, "failed preflight must retain the original")
	}
	w = callReplacement(t, owner, originalID, map[string]any{"status": "@dmrecipient new private text"})
	replacementID := decodedReplacementID(t, w)
	require.NotEqual(t, originalID, replacementID)
	recipients = nil
	require.NoError(t, conn.Where("status_id = ?", replacementID).Find(&recipients).Error)
	require.Len(t, recipients, 1)
	require.Equal(t, recipient.Id, recipients[0].AccountId)
}
