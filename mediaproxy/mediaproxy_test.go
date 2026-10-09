package mediaproxy

import (
	"net/url"
	"strings"
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) {
	t.Helper()
	old := config.C
	config.C.EncryptedKey = "0123456789abcdef-test-key"
	config.C.AppBaseURL = "https://c.test"
	config.C.MediaProxyEnabled = "true"
	t.Cleanup(func() { config.C = old })
}

func TestSignVerify(t *testing.T) {
	setup(t)
	sig := Sign(KindAvatar, "https://r.example/a.png")
	require.True(t, Verify(KindAvatar, "https://r.example/a.png", sig))
	require.False(t, Verify(KindHeader, "https://r.example/a.png", sig), "kind is signed")
	require.False(t, Verify(KindAvatar, "https://r.example/b.png", sig), "url is signed")
	require.False(t, Verify(KindAvatar, "https://r.example/a.png", ""))
	config.C.EncryptedKey = "another-secret-another-secret"
	require.False(t, Verify(KindAvatar, "https://r.example/a.png", sig), "secret is signed")
}

func TestURLRewritesOnlyRemote(t *testing.T) {
	setup(t)
	remote := "https://ovo.wxw.moe/media/a.jpeg?x=1"
	got := URL(KindMedia, remote)
	require.True(t, strings.HasPrefix(got, "https://c.test/proxy/media/"), got)
	u, err := url.Parse(got)
	require.NoError(t, err)
	require.Equal(t, KindMedia, u.Query().Get("kind"))
	parts := strings.Split(strings.TrimPrefix(u.Path, "/proxy/media/"), "/")
	require.Len(t, parts, 2)
	decoded, ok := Decode(parts[1])
	require.True(t, ok)
	require.Equal(t, remote, decoded)
	require.True(t, Verify(KindMedia, decoded, parts[0]))

	for _, same := range []string{"", "/media/1/a.png", "https://c.test/media/1/a.png", "HTTPS://C.TEST/emoji-assets/x.png", "data:image/png;base64,AAAA", "ftp://x.example/a", strings.Repeat("https://r.example/", 200)} {
		require.Equal(t, same, URL(KindMedia, same), same)
	}
	require.Equal(t, remote, URL("bogus", remote))

	config.C.MediaProxyEnabled = "false"
	require.Equal(t, remote, URL(KindMedia, remote))
}

func TestDecodeRejectsGarbage(t *testing.T) {
	_, ok := Decode("%%%")
	require.False(t, ok)
	_, ok = Decode("")
	require.False(t, ok)
}

func TestLimits(t *testing.T) {
	setup(t)
	require.EqualValues(t, 20<<20, MaxBytes())
	require.EqualValues(t, 512<<20, MaxStreamBytes())
	config.C.MediaProxyMaxBytes = "1000"
	require.EqualValues(t, 1000, MaxBytes())
}
