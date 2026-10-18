package systemsettings

import (
	"net/url"
	"sync/atomic"
)

// staticVersion is the frontend build id read from frontend/dist/version.json
// at startup (the single source of the version; nothing is compiled in).
var staticVersion atomic.Value // string

// SetStaticVersion records the frontend build id.
func SetStaticVersion(v string) { staticVersion.Store(v) }

// VersionedStatic appends ?v=<build id> to a non-hashed frontend file path
// (icons, logo) so caches pick up a new copy after each deploy. Paths are
// returned unchanged when no build id is known.
func VersionedStatic(path string) string {
	v, _ := staticVersion.Load().(string)
	if v == "" {
		return path
	}
	return path + "?v=" + url.QueryEscape(v)
}
