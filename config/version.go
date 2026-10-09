package config

// Version information.
const (
	Version     = "1.0.0"
	MastodonAPI = "4.3.0"

	// MastodonAPIVersionNumber is the integer api_versions.mastodon value
	// advertised by /api/v2/instance.
	MastodonAPIVersionNumber = 2
)

// GetVersion returns the current application version.
func GetVersion() string {
	return Version
}

// GetMastodonAPIVersion returns the compatible Mastodon API version.
func GetMastodonAPIVersion() string {
	return MastodonAPI
}

// GetInstanceVersion returns the version string advertised by the Mastodon
// instance endpoints: the compatible Mastodon version plus a semver build
// suffix (e.g. "4.3.0+community"). Mastodon clients gate features on it.
func GetInstanceVersion() string {
	return MastodonAPI + "+community"
}
