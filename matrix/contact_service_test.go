package matrix

import (
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ====================================================================
// ContactService construction
// ====================================================================

func TestNewContactService(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x", ASToken: "a"})
	cs := NewContactService(c)
	require.NotNil(t, cs)
	assert.Equal(t, c, cs.client)
}

// ====================================================================
// LookupLocalAccount
// ====================================================================

func TestContactService_LookupLocalAccount_InvalidID(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)
	setupTestGlobalConfig("http://unused")

	_, err := cs.LookupLocalAccount("invalid-id")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid")
}

func TestContactService_LookupLocalAccount_NotInNamespace(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)
	setupTestGlobalConfig("http://unused")

	_, err := cs.LookupLocalAccount("@bob:example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a local user namespace")
}

func TestContactService_LookupLocalAccount_EmptyUsername(t *testing.T) {
	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)
	setupTestGlobalConfig("http://unused")

	// Just the prefix with no username after it.
	_, err := cs.LookupLocalAccount("@_c_:example.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty username")
}

func TestContactService_LookupLocalAccount_DBHit(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "alice"}
	require.NoError(t, db.Create(account).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	got, err := cs.LookupLocalAccount("@_c_alice:example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, account.Id, got.Id)
	assert.Equal(t, "alice", got.Username)
}

func TestContactService_LookupLocalAccount_NotFound(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	// Valid format but user doesn't exist -> returns nil, nil.
	got, err := cs.LookupLocalAccount("@_c_nonexistent:example.com")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// ====================================================================
// AddContact
// ====================================================================

func TestContactService_AddContact(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "owner"}
	require.NoError(t, db.Create(account).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	matrixID := "@bob:example.com"
	err := cs.AddContact(account.Id, matrixID, "Bob")
	require.NoError(t, err)

	// Verify the contact was created.
	var contact models.Contact
	require.NoError(t, db.Where("account_id = ?", account.Id).First(&contact).Error)
	assert.Equal(t, models.ContactTypeMatrix, contact.ContactType)
	assert.Equal(t, "Bob", contact.DisplayName)
	assert.Equal(t, models.ContactStatusAccepted, contact.Status)
	require.NotNil(t, contact.ContactMatrixId)
	assert.Equal(t, matrixID, *contact.ContactMatrixId)
}

// ====================================================================
// CreateContactRequest / AcceptContactRequest / RejectContactRequest
// ====================================================================

func TestContactService_CreateContactRequest(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "recipient"}
	require.NoError(t, db.Create(account).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	req, err := cs.CreateContactRequest("@sender:example.com", account.Id, "hi")
	require.NoError(t, err)
	require.NotNil(t, req)
	assert.Equal(t, models.ContactStatusPending, req.Status)
	require.NotNil(t, req.FromMatrixUserId)
	assert.Equal(t, "@sender:example.com", *req.FromMatrixUserId)
}

func TestContactService_CreateContactRequest_DuplicateReturnsExisting(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "dup"}
	require.NoError(t, db.Create(account).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	// First request.
	req1, err := cs.CreateContactRequest("@dup:example.com", account.Id, "first")
	require.NoError(t, err)

	// Second request from same user -> returns existing.
	req2, err := cs.CreateContactRequest("@dup:example.com", account.Id, "second")
	require.NoError(t, err)
	assert.Equal(t, req1.Id, req2.Id)
}

func TestContactService_AcceptContactRequest(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "accepter"}
	require.NoError(t, db.Create(account).Error)

	fromMatrix := "@fromaccept:example.com"
	req := &models.ContactRequest{
		FromMatrixUserId: &fromMatrix,
		ToAccountId:      &account.Id,
		Message:          "please",
		Status:           models.ContactStatusPending,
	}
	require.NoError(t, db.Create(req).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	err := cs.AcceptContactRequest(req.Id, account.Id)
	require.NoError(t, err)

	// Verify request updated.
	var updated models.ContactRequest
	require.NoError(t, db.First(&updated, req.Id).Error)
	assert.Equal(t, models.ContactStatusAccepted, updated.Status)

	// Verify contact created.
	var contact models.Contact
	require.NoError(t, db.Where("account_id = ?", account.Id).First(&contact).Error)
	assert.Equal(t, models.ContactTypeMatrix, contact.ContactType)
	assert.Equal(t, models.ContactStatusAccepted, contact.Status)
}

func TestContactService_AcceptContactRequest_NotFound(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	err := cs.AcceptContactRequest(99999, 1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "request not found")
}

func TestContactService_RejectContactRequest(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "rejecter"}
	require.NoError(t, db.Create(account).Error)

	fromMatrix := "@fromreject:example.com"
	req := &models.ContactRequest{
		FromMatrixUserId: &fromMatrix,
		ToAccountId:      &account.Id,
		Status:           models.ContactStatusPending,
	}
	require.NoError(t, db.Create(req).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	err := cs.RejectContactRequest(req.Id, account.Id)
	require.NoError(t, err)

	// Verify status updated to blocked.
	var updated models.ContactRequest
	require.NoError(t, db.First(&updated, req.Id).Error)
	assert.Equal(t, models.ContactStatusBlocked, updated.Status)
}

// ====================================================================
// GetContacts / GetPendingContactRequests
// ====================================================================

func TestContactService_GetContacts(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "contactowner"}
	require.NoError(t, db.Create(account).Error)

	matrixID1 := "@c1:example.com"
	matrixID2 := "@c2:example.com"
	contacts := []*models.Contact{
		{AccountId: account.Id, ContactType: models.ContactTypeMatrix, ContactMatrixId: &matrixID1, Status: models.ContactStatusAccepted},
		{AccountId: account.Id, ContactType: models.ContactTypeMatrix, ContactMatrixId: &matrixID2, Status: models.ContactStatusAccepted},
		{AccountId: account.Id, ContactType: models.ContactTypeMatrix, ContactMatrixId: &matrixID1, Status: models.ContactStatusBlocked},
	}
	for _, ct := range contacts {
		require.NoError(t, db.Create(ct).Error)
	}

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	got, err := cs.GetContacts(account.Id)
	require.NoError(t, err)
	// Only accepted contacts returned.
	assert.Len(t, got, 2)
}

func TestContactService_GetPendingContactRequests(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "pendowner"}
	require.NoError(t, db.Create(account).Error)

	fromMatrix := "@pending:example.com"
	reqs := []*models.ContactRequest{
		{FromMatrixUserId: &fromMatrix, ToAccountId: &account.Id, Status: models.ContactStatusPending},
		{FromMatrixUserId: &fromMatrix, ToAccountId: &account.Id, Status: models.ContactStatusAccepted},
	}
	for _, r := range reqs {
		require.NoError(t, db.Create(r).Error)
	}

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	got, err := cs.GetPendingContactRequests(account.Id)
	require.NoError(t, err)
	assert.Len(t, got, 1)
}

// ====================================================================
// RemoveContact
// ====================================================================

func TestContactService_RemoveContact(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "remover"}
	require.NoError(t, db.Create(account).Error)

	matrixID := "@remove:example.com"
	contact := &models.Contact{
		AccountId:       account.Id,
		ContactType:     models.ContactTypeMatrix,
		ContactMatrixId: &matrixID,
		Status:          models.ContactStatusAccepted,
	}
	require.NoError(t, db.Create(contact).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	err := cs.RemoveContact(account.Id, contact.Id)
	require.NoError(t, err)

	// Verify it's gone.
	var count int64
	db.Model(&models.Contact{}).Where("id = ?", contact.Id).Count(&count)
	assert.Equal(t, int64(0), count)
}

// ====================================================================
// BlockMatrixUser / IsBlocked
// ====================================================================

func TestContactService_BlockMatrixUser(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "blocker"}
	require.NoError(t, db.Create(account).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	err := cs.BlockMatrixUser(account.Id, "@blocked:example.com")
	require.NoError(t, err)

	// Verify it's blocked.
	blocked, err := cs.IsBlocked(account.Id, "@blocked:example.com")
	require.NoError(t, err)
	assert.True(t, blocked)
}

func TestContactService_IsBlocked_NotBlocked(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "checkblocked"}
	require.NoError(t, db.Create(account).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	blocked, err := cs.IsBlocked(account.Id, "@notblocked:example.com")
	require.NoError(t, err)
	assert.False(t, blocked)
}

// ====================================================================
// GetLocalUserMatrixID / GetLocalUserMatrixIDByUsername
// ====================================================================

func TestContactService_GetLocalUserMatrixID(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	account := &models.Account{Username: "matrixiduser"}
	require.NoError(t, db.Create(account).Error)

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	matrixID := cs.GetLocalUserMatrixID(account.Id)
	assert.Equal(t, "@_c_matrixiduser:example.com", matrixID)
}

func TestContactService_GetLocalUserMatrixID_Fallback(t *testing.T) {
	db := setupMatrixTestDB(t)
	truncateMatrixTables(t, db)
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	// Non-existent account -> falls back to using the numeric ID.
	matrixID := cs.GetLocalUserMatrixID(99999)
	assert.Contains(t, matrixID, "@_c_99999:example.com")
}

func TestContactService_GetLocalUserMatrixIDByUsername(t *testing.T) {
	setupTestGlobalConfig("http://unused")

	c := NewASClientWithConfig(&Config{HomeserverURL: "http://x"})
	cs := NewContactService(c)

	matrixID := cs.GetLocalUserMatrixIDByUsername("carol")
	assert.Equal(t, "@_c_carol:example.com", matrixID)
}
