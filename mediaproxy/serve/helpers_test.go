package serve

import (
	"net/http"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/avatarcache"
)

var (
	time0        = time.Unix(1700000000, 0)
	realValidate = activitypub.ValidateURI
	realClient   = func() *http.Client { return activitypub.NewSafeHTTPClient(0, maxRedirects) }
)

// Relay tests run without the avatar disk cache unless they install one.
func init() { avatarCache = func() *avatarcache.Cache { return nil } }
