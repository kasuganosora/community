package serializer

import (
	"testing"

	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestAPIFieldsRenderMastodonValues(t *testing.T) {
	opts := htmlsafe.OptionsForBaseURL("https://c.example")
	local := toAPIFields([]models.AccountField{{Name: "Site", Val: "https://e.com <b>"}}, true, opts)
	require.Len(t, local, 1)
	require.Contains(t, local[0].Value, `<a href="https://e.com"`)
	require.Contains(t, local[0].Value, "&lt;b&gt;")
	require.NotContains(t, local[0].Value, "<p>")

	remote := toAPIFields([]models.AccountField{{Name: "<i>Web</i>", Val: `<a href="https://r.example" onmouseover="x()">r</a><img src=x onerror=alert(1)>`}}, false, opts)
	require.Equal(t, "Web", remote[0].Name)
	require.Contains(t, remote[0].Value, `href="https://r.example"`)
	require.NotContains(t, remote[0].Value, "onmouseover")
	require.NotContains(t, remote[0].Value, "onerror")
}
