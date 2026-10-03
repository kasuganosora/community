package homeserver

import (
	"github.com/kasuganosora/community/activitypub"
	"net/http"
)

// Media targets originate in client MXC URLs. Their network boundary must be
// checked at dial time, including all DNS answers; unrelated federation request
// behavior is preserved by selecting this client only for media routes.
func (c *FederationClient) federationMediaHTTPClient() *http.Client {
	c.fedHTTPClientMu.Lock()
	defer c.fedHTTPClientMu.Unlock()
	if c.fedMediaHTTPClient == nil {
		c.fedMediaHTTPClient = activitypub.NewAuthenticatedMediaHTTPClient(federationTLSConfig("/complement/ca/ca.crt"))
	}
	return c.fedMediaHTTPClient
}
