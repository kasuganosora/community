package activitypub

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActivityPubError(t *testing.T) {
	err := &ActivityPubError{Message: "test error"}
	assert.Equal(t, "test error", err.Error())
	assert.Implements(t, (*error)(nil), err)
}

func TestErrConstants(t *testing.T) {
	assert.Equal(t, "invalid acct format", ErrInvalidAcctFormat.Error())
	assert.Equal(t, "webfinger discovery failed", ErrWebFingerFailed.Error())
	assert.Equal(t, "actor not found", ErrActorNotFound.Error())
}

func TestActivityPubErrorTypes(t *testing.T) {
	// Verify they are distinct errors
	assert.NotEqual(t, ErrInvalidAcctFormat, ErrWebFingerFailed)
	assert.NotEqual(t, ErrWebFingerFailed, ErrActorNotFound)
	assert.NotEqual(t, ErrInvalidAcctFormat, ErrActorNotFound)
}
