package serve

import (
	"net/http"
	"time"

	"github.com/kasuganosora/community/activitypub"
)

var (
	time0        = time.Unix(1700000000, 0)
	realValidate = activitypub.ValidateURI
	realClient   = func() *http.Client { return activitypub.NewSafeHTTPClient(0, maxRedirects) }
)
