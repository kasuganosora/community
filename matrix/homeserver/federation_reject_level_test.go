package homeserver

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFederationRejectIsRoutine(t *testing.T) {
	require.True(t, federationRejectIsRoutine("org.matrix.dummy_event", errors.New("auth failed")))
	require.True(t, federationRejectIsRoutine("m.room.message", fmt.Errorf("auth event: %w", gorm.ErrRecordNotFound)))
	require.False(t, federationRejectIsRoutine("m.room.message", errors.New("invalid signature")))
}
