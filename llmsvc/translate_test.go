package llmsvc

import (
	"strings"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/require"
)

func TestProtectRestoreKeepsEntities(t *testing.T) {
	pr := &protector{}
	in := `<p>Hello <span class="h-card"><a href="https://social.example/@alice" class="u-url mention">@<span>alice</span></a></span> see <a href="https://example.org/x">link</a> <code>go test ./...</code> :blobcat: #FediTag @bob@remote.example it&#39;s {{7}}</p>`
	out := pr.protect(in)
	for _, keep := range []string{"alice", "go test", ":blobcat:", "#FediTag", "@bob@remote.example", "example.org"} {
		require.NotContains(t, out, keep, "should be protected: %s", keep)
	}
	require.Contains(t, out, "&#39;", "entities must not be treated as hashtags")
	require.Contains(t, out, "Hello")
	// The model translates text but keeps placeholders; dropped ones are appended.
	translated := strings.Replace(out, "Hello", "Bonjour", 1)
	require.Equal(t, strings.Replace(in, "Hello", "Bonjour", 1), pr.restore(translated, tokensIn(out)))
	dropped := strings.Replace(translated, "{{0}}", "", 1)
	require.Contains(t, pr.restore(dropped, tokensIn(out)), "{{7}}")
}

func TestAPIKeyEncryptedAndMasked(t *testing.T) {
	old := config.C.EncryptedKey
	config.C.EncryptedKey = "unit-test-key-0123456789abcdef"
	t.Cleanup(func() { config.C.EncryptedKey = old })
	p := ProviderConfig{ID: "p1", Name: "Example", Type: TypeOpenAI, Model: "example-model"}
	require.NoError(t, p.SetAPIKey("sk-example-secret-1234"))
	require.NotContains(t, p.APIKeyEnc, "sk-example")
	plain, err := p.apiKey()
	require.NoError(t, err)
	require.Equal(t, "sk-example-secret-1234", plain)
	pub := p.Public()
	require.True(t, pub.APIKeySet)
	require.Equal(t, "••••••••1234", pub.APIKeyMask)
	require.Empty(t, pub.ProviderConfig.APIKeyEnc)
}

func TestValidate(t *testing.T) {
	ok := Config{Providers: []ProviderConfig{{ID: "a", Name: "A", Type: TypeAnthropic, Model: "m"}}, Services: map[string]string{ServiceTranslation: "a"}}
	require.NoError(t, Validate(ok))
	bad := ok
	bad.Services = map[string]string{ServiceTranslation: "missing"}
	require.Error(t, Validate(bad))
	compat := Config{Providers: []ProviderConfig{{ID: "a", Name: "A", Type: TypeOpenAICompatible, Model: "m"}}}
	require.Error(t, Validate(compat), "openai_compatible needs a base URL")
}
