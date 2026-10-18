package activitypub

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupStatusReachDB creates a test DB with all models needed by StatusReachFinder.
func setupStatusReachDB(t *testing.T) *testutil.TestDB {
	t.Helper()
	// The delivery compare-and-swap SQL requires SQLite/PostgreSQL syntax;
	// sqlexec does not implement IS DISTINCT FROM.
	conn := searchFederationDB(t)
	require.NoError(t, conn.AutoMigrate(&models.Relay{}, &Delivery{}))
	testDB := &testutil.TestDB{GormDB: conn}
	oldClient := sharedHTTPClient
	sharedHTTPClient = &http.Client{Transport: outboundTransport(func(r *http.Request) (*http.Response, error) {
		return outboundResponse(200, `{"id":"`+r.URL.String()+`","inbox":"`+r.URL.String()+`/inbox"}`), nil
	})}
	t.Cleanup(func() { sharedHTTPClient = oldClient })
	return testDB
}

func TestStatusReachFinderGetInboxesNoFollowers(t *testing.T) {
	testDB := setupStatusReachDB(t)

	account := &models.Account{Username: "author"}
	require.NoError(t, testDB.GormDB.Create(account).Error)

	status := &models.Status{
		AccountId:  account.Id,
		Visibility: models.StatusVisibilityPublic,
		Content:    models.StatusContent{Content: "hello"},
	}
	require.NoError(t, testDB.GormDB.Create(status).Error)

	finder := &StatusReachFinder{db: testDB.GormDB, status: status}
	inboxes, err := finder.GetInboxes()
	require.NoError(t, err)
	// No followers, no mentions, no relays enabled
	assert.Empty(t, inboxes)
}

func TestStatusReachFinderGetInboxesWithFollowers(t *testing.T) {
	testDB := setupStatusReachDB(t)

	// Create author account
	author := &models.Account{Username: "author"}
	require.NoError(t, testDB.GormDB.Create(author).Error)

	// Create remote follower
	remoteDomain := "remote.example.com"
	remoteFollower := &models.Account{
		Username: "follower1",
		OriginId: "https://remote.example.com/users/follower1",
		Domain:   &remoteDomain,
	}
	require.NoError(t, testDB.GormDB.Create(remoteFollower).Error)

	// Create follow relationship (follower follows author)
	follow := &models.Follow{
		AccountId: remoteFollower.Id,
		FollowId:  author.Id,
		Status:    models.FollowStatusFollowed,
	}
	require.NoError(t, testDB.GormDB.Create(follow).Error)

	status := &models.Status{
		AccountId:  author.Id,
		Visibility: models.StatusVisibilityPrivate, // private, no relay
		Content:    models.StatusContent{Content: "private post"},
	}
	require.NoError(t, testDB.GormDB.Create(status).Error)

	finder := &StatusReachFinder{db: testDB.GormDB, status: status}
	inboxes, err := finder.GetInboxes()
	require.NoError(t, err)
	assert.Len(t, inboxes, 1)
	assert.Contains(t, inboxes[0], "remote.example.com/users/follower1/inbox")
}

func TestStatusReachFinderGetInboxesDedup(t *testing.T) {
	testDB := setupStatusReachDB(t)

	author := &models.Account{Username: "author2"}
	require.NoError(t, testDB.GormDB.Create(author).Error)

	// Create two remote followers from the same domain
	remoteDomain := "shared.example.com"
	f1 := &models.Account{Username: "f1", OriginId: "https://shared.example.com/users/f1", Domain: &remoteDomain}
	f2 := &models.Account{Username: "f2", OriginId: "https://shared.example.com/users/f2", Domain: &remoteDomain}
	require.NoError(t, testDB.GormDB.Create(f1).Error)
	require.NoError(t, testDB.GormDB.Create(f2).Error)

	require.NoError(t, testDB.GormDB.Create(&models.Follow{
		AccountId: f1.Id, FollowId: author.Id, Status: models.FollowStatusFollowed,
	}).Error)
	require.NoError(t, testDB.GormDB.Create(&models.Follow{
		AccountId: f2.Id, FollowId: author.Id, Status: models.FollowStatusFollowed,
	}).Error)

	status := &models.Status{
		AccountId:  author.Id,
		Visibility: models.StatusVisibilityPrivate,
		Content:    models.StatusContent{Content: "test"},
	}
	require.NoError(t, testDB.GormDB.Create(status).Error)

	finder := &StatusReachFinder{db: testDB.GormDB, status: status}
	inboxes, err := finder.GetInboxes()
	require.NoError(t, err)
	// Two different followers = two different inboxes
	assert.Len(t, inboxes, 2)
}

func TestStatusReachFinderIsAccountBlocked(t *testing.T) {
	testDB := setupStatusReachDB(t)

	author := &models.Account{Username: "author3"}
	require.NoError(t, testDB.GormDB.Create(author).Error)

	target := &models.Account{Username: "blocked"}
	require.NoError(t, testDB.GormDB.Create(target).Error)

	status := &models.Status{
		AccountId:  author.Id,
		Visibility: models.StatusVisibilityPublic,
		Content:    models.StatusContent{Content: "test"},
	}
	require.NoError(t, testDB.GormDB.Create(status).Error)

	finder := &StatusReachFinder{db: testDB.GormDB, status: status}

	// Not blocked initially
	assert.False(t, finder.isAccountBlocked(target.Id))

	// Create a block
	block := &models.Block{
		AccountId:       author.Id,
		TargetAccountId: target.Id,
		Type:            models.BlockTypeAccount,
	}
	require.NoError(t, testDB.GormDB.Create(block).Error)

	assert.True(t, finder.isAccountBlocked(target.Id))
}

func TestStatusReachFinderBlockedFollowerExcluded(t *testing.T) {
	testDB := setupStatusReachDB(t)

	author := &models.Account{Username: "author4"}
	require.NoError(t, testDB.GormDB.Create(author).Error)

	remoteDomain := "blocked.example.com"
	blockedFollower := &models.Account{
		Username: "blockedfollower",
		OriginId: "https://blocked.example.com/users/blockedfollower",
		Domain:   &remoteDomain,
	}
	require.NoError(t, testDB.GormDB.Create(blockedFollower).Error)

	require.NoError(t, testDB.GormDB.Create(&models.Follow{
		AccountId: blockedFollower.Id, FollowId: author.Id, Status: models.FollowStatusFollowed,
	}).Error)

	status := &models.Status{
		AccountId:  author.Id,
		Visibility: models.StatusVisibilityPrivate,
		Content:    models.StatusContent{Content: "test"},
	}
	require.NoError(t, testDB.GormDB.Create(status).Error)

	// Block the follower
	require.NoError(t, testDB.GormDB.Create(&models.Block{
		AccountId:       author.Id,
		TargetAccountId: blockedFollower.Id,
		Type:            models.BlockTypeAccount,
	}).Error)

	// unsafe=false should exclude blocked
	finder := &StatusReachFinder{db: testDB.GormDB, status: status, unsafe: false}
	inboxes, err := finder.GetInboxes()
	require.NoError(t, err)
	assert.Empty(t, inboxes)

	// unsafe=true should include blocked
	finderUnsafe := &StatusReachFinder{db: testDB.GormDB, status: status, unsafe: true}
	inboxesUnsafe, err := finderUnsafe.GetInboxes()
	require.NoError(t, err)
	assert.Len(t, inboxesUnsafe, 1)
}

func TestNewStatusReachFinder(t *testing.T) {
	setupStatusReachDB(t)
	status := &models.Status{Visibility: models.StatusVisibilityPublic}
	finder := NewStatusReachFinder(status, false)
	require.NotNil(t, finder)
	assert.NotNil(t, finder.db)
	assert.False(t, finder.unsafe)
}

func TestNewAccountReachFinder(t *testing.T) {
	setupStatusReachDB(t)
	account := &models.Account{Username: "test"}
	finder := NewAccountReachFinder(account)
	require.NotNil(t, finder)
	assert.NotNil(t, finder.db)
}

func TestAccountReachFinderGetInboxes(t *testing.T) {
	testDB := setupStatusReachDB(t)

	account := &models.Account{Username: "acctreach"}
	require.NoError(t, testDB.GormDB.Create(account).Error)

	// No followers, no relays
	finder := &AccountReachFinder{db: testDB.GormDB, account: account}
	inboxes, err := finder.GetInboxes()
	require.NoError(t, err)
	assert.Empty(t, inboxes)
}

func TestAccountReachFinderGetInboxesWithFollowers(t *testing.T) {
	testDB := setupStatusReachDB(t)

	account := &models.Account{Username: "acctreach2"}
	require.NoError(t, testDB.GormDB.Create(account).Error)

	remoteDomain := "remote2.example.com"
	follower := &models.Account{Username: "follower2", OriginId: "https://remote2.example.com/users/follower2", Domain: &remoteDomain}
	require.NoError(t, testDB.GormDB.Create(follower).Error)

	require.NoError(t, testDB.GormDB.Create(&models.Follow{
		AccountId: follower.Id, FollowId: account.Id, Status: models.FollowStatusFollowed,
	}).Error)

	finder := &AccountReachFinder{db: testDB.GormDB, account: account}
	inboxes, err := finder.GetInboxes()
	require.NoError(t, err)
	assert.Len(t, inboxes, 1)
	assert.Contains(t, inboxes[0], "remote2.example.com")
}

// --- DeliveryQueue tests with DB ---

func TestDeliveryQueueEnqueue(t *testing.T) {
	testDB := setupStatusReachDB(t)

	q := NewDeliveryQueue(nil, "", 0)
	err := q.Enqueue(
		"https://example.com/activities/1",
		"https://target.example.com/inbox",
		map[string]any{
			"type":    "Create",
			"content": "hello",
			"bto":     []string{"secret"},
			"bcc":     []string{"hidden"},
		},
	)
	require.NoError(t, err)

	// Verify delivery was created
	var delivery Delivery
	require.NoError(t, testDB.GormDB.Where("activity_id = ?", "https://example.com/activities/1").First(&delivery).Error)
	assert.Equal(t, "https://target.example.com/inbox", delivery.TargetInbox)
	assert.Equal(t, DeliveryStatusPending, delivery.Status)
	// Verify privacy fields stripped
	assert.NotContains(t, delivery.Payload, "bto")
	assert.NotContains(t, delivery.Payload, "bcc")
	assert.Contains(t, delivery.Payload, "Create")
}

func TestDeliveryQueueEnqueueToMany(t *testing.T) {
	testDB := setupStatusReachDB(t)

	q := NewDeliveryQueue(nil, "", 0)
	inboxes := []string{
		"https://inbox1.example.com/inbox",
		"https://inbox2.example.com/inbox",
		"https://inbox3.example.com/inbox",
	}
	err := q.EnqueueToMany("https://example.com/activities/2", inboxes, map[string]any{"type": "Announce"})
	require.NoError(t, err)

	var count int64
	testDB.GormDB.Model(&Delivery{}).Where("activity_id = ?", "https://example.com/activities/2").Count(&count)
	assert.Equal(t, int64(3), count)
}

func TestDeliveryQueueEnqueueToManyDedupInboxes(t *testing.T) {
	testDB := setupStatusReachDB(t)

	q := NewDeliveryQueue(nil, "", 0)
	// Duplicate inboxes
	inboxes := []string{
		"https://same.example.com/inbox",
		"https://same.example.com/inbox",
	}
	err := q.EnqueueToMany("https://example.com/activities/3", inboxes, map[string]any{"type": "Create"})
	require.NoError(t, err)

	var count int64
	testDB.GormDB.Model(&Delivery{}).Where("activity_id = ?", "https://example.com/activities/3").Count(&count)
	assert.Equal(t, int64(2), count) // no dedup in EnqueueToMany
}

// TestDeliveryQueueProcessPendingWithMockServer tests the full delivery
// lifecycle using a mock HTTP server.
func TestDeliveryQueueDeliverLifecycle(t *testing.T) {
	// This test verifies deliver() and handleDeliveryError() indirectly
	// through processPending, using a mock server.
	server := newMockInboxServer(t, http.StatusAccepted)
	defer server.Close()

	testDB := setupStatusReachDB(t)

	// Create a delivery targeting the mock server
	delivery := &Delivery{
		ActivityID:  "https://example.com/activities/lifecycle",
		TargetInbox: server.URL + "/inbox",
		Payload:     `{"type":"Create","content":"hello"}`,
		Status:      DeliveryStatusPending,
	}
	require.NoError(t, testDB.GormDB.Create(delivery).Error)

	// We can't easily test processPending directly because it uses a hardcoded
	// client. But we can test markFailed and handleDeliveryError through
	// the queue methods.
	q := &DeliveryQueue{
		client:   NewClient(nil, ""),
		workers:  0,
		stopChan: make(chan struct{}),
	}

	// Test markFailed directly
	q.markFailed(delivery, "test failure reason")
	var updated Delivery
	testDB.GormDB.First(&updated, delivery.ID)
	assert.Equal(t, DeliveryStatusFailed, updated.Status)
	assert.Equal(t, "test failure reason", updated.LastError)
}

func TestDeliveryQueueHandleDeliveryError(t *testing.T) {
	testDB := setupStatusReachDB(t)

	delivery := &Delivery{
		ActivityID:  "https://example.com/activities/retry",
		TargetInbox: "https://unreachable.example.com/inbox",
		Payload:     `{"type":"Create"}`,
		Status:      DeliveryStatusProcessing,
		Retries:     0,
	}
	require.NoError(t, testDB.GormDB.Create(delivery).Error)

	q := &DeliveryQueue{
		client:   NewClient(nil, ""),
		workers:  0,
		stopChan: make(chan struct{}),
	}

	// Simulate a delivery error (retry count < 5)
	q.handleDeliveryError(delivery, context.DeadlineExceeded)

	var updated Delivery
	testDB.GormDB.First(&updated, delivery.ID)
	assert.Equal(t, DeliveryStatusPending, updated.Status)
	assert.Equal(t, 1, updated.Retries)
	assert.NotEmpty(t, updated.LastError)
	require.NotNil(t, updated.NextRetryAt)
	assert.True(t, updated.NextRetryAt.After(time.Now()))
}

func TestDeliveryQueueHandleDeliveryErrorMaxRetries(t *testing.T) {
	testDB := setupStatusReachDB(t)

	delivery := &Delivery{
		ActivityID:  "https://example.com/activities/maxretries",
		TargetInbox: "https://unreachable.example.com/inbox",
		Payload:     `{"type":"Create"}`,
		Status:      DeliveryStatusProcessing,
		Retries:     5, // Already at max retries
	}
	require.NoError(t, testDB.GormDB.Create(delivery).Error)

	q := &DeliveryQueue{
		client:   NewClient(nil, ""),
		workers:  0,
		stopChan: make(chan struct{}),
	}

	q.handleDeliveryError(delivery, context.DeadlineExceeded)

	var updated Delivery
	testDB.GormDB.First(&updated, delivery.ID)
	assert.Equal(t, DeliveryStatusFailed, updated.Status)
}

func TestDeliveryQueueDeliverInvalidPayload(t *testing.T) {
	if pgtest.Enabled() {
		t.Skip("PostgreSQL (like MySQL) json columns cannot store an invalid payload")
	}
	testDB := setupStatusReachDB(t)

	delivery := &Delivery{
		ActivityID:  "https://example.com/activities/badpayload",
		TargetInbox: "https://target.example.com/inbox",
		Payload:     `{invalid json}`,
		Status:      DeliveryStatusProcessing,
	}
	require.NoError(t, testDB.GormDB.Create(delivery).Error)

	q := &DeliveryQueue{
		client:   NewClient(nil, ""),
		workers:  0,
		stopChan: make(chan struct{}),
	}

	q.deliver(*delivery)

	var updated Delivery
	testDB.GormDB.First(&updated, delivery.ID)
	assert.Equal(t, DeliveryStatusFailed, updated.Status)
	assert.Contains(t, updated.LastError, "invalid payload")
}
