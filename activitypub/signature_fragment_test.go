package activitypub

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSignatureKeyFragmentPreservesExactActorDelegation(t *testing.T) {
	const uri = "https://mastodon.example/ap/users/123"
	for _, fragment := range []string{"#main-key", "#rsa-2d1bfa24a05cd8d9", "#rotated%2Dkey", ""} {
		keyID := uri + fragment
		got, err := actorURIFromKeyID(keyID)
		require.NoError(t, err)
		require.Equal(t, uri, got)
		actor := &Actor{ID: uri, PublicKey: &PublicKey{ID: keyID, Owner: uri, PublicKeyPem: "present"}}
		require.NoError(t, validateActorSigningKey(actor, got, keyID))
		for _, field := range []string{"actor", "key", "owner"} {
			for _, bad := range []string{"", "https://attacker.example/users/other"} {
				clone := *actor
				key := *actor.PublicKey
				clone.PublicKey = &key
				switch field {
				case "actor":
					clone.ID = bad
				case "key":
					key.ID = bad
				case "owner":
					key.Owner = bad
				}
				require.Error(t, validateActorSigningKey(&clone, got, keyID), field)
			}
		}
		require.Error(t, validateActorSigningKey(actor, got, keyID+"other"))
	}
	for _, keyID := range []string{"ftp://example.com/users/a#rsa", "https://example.com/%zz#rsa"} {
		_, err := actorURIFromKeyID(keyID)
		require.Error(t, err, keyID)
	}
}

func TestSignatureFragmentDoesNotBypassSSRF(t *testing.T) {
	t.Setenv(labPeersEnv, "")
	_, err := NewSignatureVerifier().getPublicKey(context.Background(), "https://127.0.0.1/users/a#rsa-rotated")
	require.Error(t, err)
}
