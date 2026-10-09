package clientip

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/require"
)

func engine(t *testing.T, proxies, headers string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	old := config.C
	config.C.TrustedProxies, config.C.TrustedProxyHeaders = proxies, headers
	t.Cleanup(func() { config.C = old })
	r := gin.New()
	require.NoError(t, Configure(r))
	r.GET("/", func(c *gin.Context) { c.String(200, c.ClientIP()) })

	return r
}

func ip(r http.Handler, peer string, hdr map[string]string) string {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = peer
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	return w.Body.String()
}

func TestDefaults(t *testing.T) {
	require.Equal(t, []string{"127.0.0.1", "::1", "172.16.0.0/12", "10.0.0.0/8", "192.168.0.0/16"}, TrustedProxies())
	require.Equal(t, []string{"X-Real-Ip", "X-Forwarded-For"}, Headers())
}

func TestUntrustedPeerCannotForgeAddress(t *testing.T) {
	r := engine(t, "", "")
	forged := map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "5.6.7.8"}
	require.Equal(t, "203.0.113.9", ip(r, "203.0.113.9:5000", forged))
	require.Equal(t, "2001:db8::9", ip(r, "[2001:db8::9]:5000", forged))
	require.Equal(t, "203.0.113.9", ip(r, "203.0.113.9:5000", nil))
}

func TestTrustedProxyUsesRealIP(t *testing.T) {
	r := engine(t, "", "")
	// nginx: X-Real-IP is $remote_addr; the client-supplied XFF stays in front.
	hdr := map[string]string{"X-Real-IP": "198.51.100.7", "X-Forwarded-For": "1.2.3.4, 198.51.100.7"}
	for _, peer := range []string{"127.0.0.1:1", "172.26.0.1:4000", "10.1.2.3:1", "192.168.1.1:1", "[::1]:1"} {
		require.Equal(t, "198.51.100.7", ip(r, peer, hdr), peer)
	}
	require.Equal(t, "2001:db8::77", ip(r, "172.26.0.1:4000", map[string]string{"X-Real-IP": "2001:db8::77", "X-Forwarded-For": "9.9.9.9, 2001:db8::77"}))
	// A trusted peer in IPv6 loopback or ULA-style setups configured explicitly.
	r6 := engine(t, "fd00::/8", "")
	require.Equal(t, "198.51.100.7", ip(r6, "[fd00::1]:1", hdr))
	require.Equal(t, "172.26.0.1", ip(r6, "172.26.0.1:4000", hdr), "private IPv4 is not trusted when not listed")
}

func TestForgedLeadingXFFWithoutRealIPUsesRightmostUntrusted(t *testing.T) {
	r := engine(t, "", "")
	require.Equal(t, "198.51.100.7", ip(r, "172.26.0.1:1", map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.7"}))
	require.Equal(t, "198.51.100.7", ip(r, "172.26.0.1:1", map[string]string{"X-Forwarded-For": "1.2.3.4, 198.51.100.7, 10.0.0.5"}))
	require.Equal(t, "172.26.0.1", ip(r, "172.26.0.1:1", nil), "no headers: the peer itself")
}

func TestRealIPWinsOverForgedXFF(t *testing.T) {
	r := engine(t, "", "")
	require.Equal(t, "198.51.100.7", ip(r, "172.26.0.1:1", map[string]string{"X-Forwarded-For": "1.2.3.4", "X-Real-IP": "198.51.100.7"}))
}

func TestConfiguredProxiesAndHeaders(t *testing.T) {
	r := engine(t, "203.0.113.0/24", "X-Forwarded-For")
	require.Equal(t, "198.51.100.7", ip(r, "203.0.113.5:1", map[string]string{"X-Forwarded-For": "1.1.1.1, 198.51.100.7", "X-Real-IP": "9.9.9.9"}), "only listed headers")
	require.Equal(t, "172.26.0.1", ip(r, "172.26.0.1:1", map[string]string{"X-Forwarded-For": "1.1.1.1"}), "private peer no longer trusted")
}

func TestInvalidConfigIsReported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	old := config.C
	defer func() { config.C = old }()
	config.C.TrustedProxies = "not-an-ip"
	require.Error(t, Configure(gin.New()))
}
