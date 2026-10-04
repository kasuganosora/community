package oauth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// AuthorizationServerMetadata handles GET /.well-known/oauth-authorization-server (RFC 8414).
// Mastodon apps and Misskey OAuth clients use it to find the authorize and token endpoints.
func AuthorizationServerMetadata(c *gin.Context) {
	base := issuer()
	c.JSON(http.StatusOK, gin.H{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"revocation_endpoint":                   base + "/oauth/revoke",
		"registration_endpoint":                 base + "/api/v1/apps",
		"response_types_supported":              []string{"code"},
		"response_modes_supported":              []string{"query"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token", "client_credentials", "password"},
		"code_challenge_methods_supported":      []string{pkceMethodS256},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post", "none"},
		"scopes_supported":                      []string{"read", "write", "follow", "push", "admin:read", "admin:write"},
		"service_documentation":                 base + "/about",
		"miauth_endpoint":                       base + "/miauth",
	})
}
