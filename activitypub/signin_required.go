package activitypub

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
)

// verifyFetchSignature is replaceable in tests.
var verifyFetchSignature = func(c *gin.Context) error {
	_, err := VerifyActivitySignature(c, nil)
	return err
}

// signedFetchAllowed enforces "require sign-in to view" for ActivityPub
// content (statuses, outbox, follower/following/featured collections): an
// unsigned or badly signed GET answers 401, a request signed by any remote
// server is served (like Misskey). The actor document is never gated so
// remote servers can still verify our signatures and follows keep working.
// It writes the response and returns false when the request was refused.
func signedFetchAllowed(c *gin.Context, account *models.Account) bool {
	if !account.HiddenFromVisitors() {
		return true
	}
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Signature, Accept")
	if err := verifyFetchSignature(c); err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "signature required"})
		return false
	}
	return true
}

// SetFetchSignatureVerifierForTest replaces the signature check used by
// signedFetchAllowed and returns a restore function. Tests only.
func SetFetchSignatureVerifierForTest(f func(*gin.Context) error) (restore func()) {
	prev := verifyFetchSignature
	verifyFetchSignature = f
	return func() { verifyFetchSignature = prev }
}
