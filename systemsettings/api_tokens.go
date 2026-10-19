package systemsettings

import "time"

// KeyAPITokenInactivityExpiry, when true, expires API tokens (OAuth and
// personal) unused for APITokenInactivityLimit. Default off.
const KeyAPITokenInactivityExpiry = "security.api_token_inactivity_expiry"

// APITokenInactivityLimit is the idle period after which a token is expired.
const APITokenInactivityLimit = 180 * 24 * time.Hour

// APITokenInactivityExpiry reports whether the inactivity rule is enabled.
func APITokenInactivityExpiry() bool { return GetBool(KeyAPITokenInactivityExpiry, false) }
