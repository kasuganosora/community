package systemsettings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kasuganosora/community/config"
)

func withBaseURL(t *testing.T, baseURL string, configured bool, domainSetting string) {
	t.Helper()

	oldURL, oldConfigured := config.C.AppBaseURL, config.AppBaseURLConfigured()
	t.Cleanup(func() { config.SetAppBaseURL(oldURL, oldConfigured) })
	config.SetAppBaseURL(baseURL, configured)

	loaded = true
	settings = map[string]any{}
	if domainSetting != "" {
		settings[KeyDomain] = domainSetting
	}
}

func TestBaseURL_ExplicitAppBaseURLWins(t *testing.T) {
	withBaseURL(t, "https://Social.Example.org/", true, "other.example.net")

	assert.Equal(t, "https://Social.Example.org", GetBaseURL())
	assert.Equal(t, "social.example.org", GetDomain())

	warning, err := CheckBaseURL(true)
	require.NoError(t, err)
	assert.Contains(t, warning, "differs")
}

func TestBaseURL_DomainSettingWithoutAppBaseURL(t *testing.T) {
	withBaseURL(t, "http://localhost:8080", false, "legacy.example.net")

	assert.Equal(t, "https://legacy.example.net", GetBaseURL())
	assert.Equal(t, "legacy.example.net", GetDomain())

	warning, err := CheckBaseURL(true)
	require.NoError(t, err)
	assert.Contains(t, warning, "APP_BASE_URL is not set")
}

func TestBaseURL_DefaultsInDevelopment(t *testing.T) {
	withBaseURL(t, "http://localhost:8080", false, "")

	assert.Equal(t, "http://localhost:8080", GetBaseURL())
	assert.Equal(t, "localhost:8080", GetDomain())

	warning, err := CheckBaseURL(false)
	require.NoError(t, err)
	assert.Contains(t, warning, "APP_BASE_URL is not set")
}

func TestCheckBaseURL_FailsInProductionWithPlaceholder(t *testing.T) {
	for _, domain := range []string{"", PlaceholderDomain} {
		withBaseURL(t, "http://localhost:8080", false, domain)

		_, err := CheckBaseURL(true)
		require.Error(t, err, "domain setting %q", domain)
		assert.Contains(t, err.Error(), "APP_BASE_URL")
	}
}

func TestCheckBaseURL_RejectsURLWithoutScheme(t *testing.T) {
	withBaseURL(t, "social.example.org", true, "")

	_, err := CheckBaseURL(false)
	require.Error(t, err)
}
