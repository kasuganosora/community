package invitecode

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInviteCodeAfterRegisterActionFollowMe(t *testing.T) {
	assert.Equal(t, "follow_me", InviteCodeAfterRegisterActionFollowMe)
}

func TestInviteCodeAfterRegisterAction_Constant(t *testing.T) {
	// Verify the constant value
	assert.Equal(t, "follow_me", InviteCodeAfterRegisterActionFollowMe)
}
