package homeserver

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestFederationMediaRejectsURLComponentsInMXCServer(t *testing.T) {
	client := &FederationClient{}
	for _, server := range []string{"public.example:443#fragment", "public.example:443?query", "user@public.example:443", "public.example:443/extra"} {
		_, err := client.fetchAuthenticatedMedia(server, "media", "download", "")
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid media server name")
	}
}
