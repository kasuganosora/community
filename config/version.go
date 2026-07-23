package config

// Version information.
const (
	Version     = "1.0.0"
	MastodonAPI = "4.0.0"
)

// GetVersion returns the current application version.
func GetVersion() string {
	return Version
}

// GetMastodonAPIVersion returns the compatible Mastodon API version.
func GetMastodonAPIVersion() string {
	return MastodonAPI
}
