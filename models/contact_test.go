package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// Contact constants
// ============================================================================

func TestContact_TypeConstants(t *testing.T) {
	assert.Equal(t, ContactType(0), ContactTypeLocal)
	assert.Equal(t, ContactType(1), ContactTypeMatrix)
}

func TestContact_StatusConstants(t *testing.T) {
	assert.Equal(t, ContactStatus(0), ContactStatusPending)
	assert.Equal(t, ContactStatus(1), ContactStatusAccepted)
	assert.Equal(t, ContactStatus(2), ContactStatusBlocked)
}

// ============================================================================
// Contact
// ============================================================================

func TestContact_TableName(t *testing.T) {
	c := Contact{}
	assert.Equal(t, "contacts", c.TableName())
}

func TestContact_IsLocalContact(t *testing.T) {
	c := &Contact{ContactType: ContactTypeLocal}
	assert.True(t, c.IsLocalContact())

	c2 := &Contact{ContactType: ContactTypeMatrix}
	assert.False(t, c2.IsLocalContact())
}

func TestContact_IsMatrixContact(t *testing.T) {
	c := &Contact{ContactType: ContactTypeMatrix}
	assert.True(t, c.IsMatrixContact())

	c2 := &Contact{ContactType: ContactTypeLocal}
	assert.False(t, c2.IsMatrixContact())
}

// ============================================================================
// ContactRequest
// ============================================================================

func TestContactRequest_TableName(t *testing.T) {
	cr := ContactRequest{}
	assert.Equal(t, "contact_requests", cr.TableName())
}

func TestContactRequest_IsFromLocal(t *testing.T) {
	accID := uint64(1)
	cr := &ContactRequest{FromAccountId: &accID}
	assert.True(t, cr.IsFromLocal())

	cr2 := &ContactRequest{}
	assert.False(t, cr2.IsFromLocal())
}

func TestContactRequest_IsFromMatrix(t *testing.T) {
	matrixID := "@user:example.com"
	cr := &ContactRequest{FromMatrixUserId: &matrixID}
	assert.True(t, cr.IsFromMatrix())

	cr2 := &ContactRequest{}
	assert.False(t, cr2.IsFromMatrix())
}
