package serializer

import (
	"strings"
	"testing"

	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestAvatarDecorationsLocalAndLegacyFields(t *testing.T) {
	account := &models.Account{AvatarDecorationID: 2, AvatarDecorations: models.AvatarDecorationSelections{{ID: "2", Angle: 0.25, FlipH: true, OffsetX: 0.1}, {ID: "5"}}}
	out := AvatarDecorations(account)
	require.Len(t, out, 2)
	require.Equal(t, AvatarDecoration{ID: "2", URL: "/api/v1/avatar_decorations/2/image", Angle: 0.25, FlipH: true, OffsetX: 0.1}, out[0])
	require.Equal(t, uint64(2), legacyAvatarDecorationID(account))
	require.Equal(t, "/api/v1/avatar_decorations/2/image", avatarDecorationURL(account))

	legacy := &models.Account{AvatarDecorationID: 9}
	require.Equal(t, []AvatarDecoration{{ID: "9", URL: "/api/v1/avatar_decorations/9/image"}}, AvatarDecorations(legacy))
}

func TestAvatarDecorationsRemoteOnlyThroughProxy(t *testing.T) {
	domain := "misskey.example"
	account := &models.Account{Domain: &domain, AvatarDecorationID: 3, AvatarDecorations: models.AvatarDecorationSelections{{ID: "9xyz", URL: "https://misskey.example/files/deco.png", Angle: -0.1}, {ID: "noURL"}}}
	out := AvatarDecorations(account)
	if !mediaproxy.Enabled() {
		require.Empty(t, out)
		return
	}
	require.Len(t, out, 1)
	require.Equal(t, mediaproxy.URL(mediaproxy.KindEmoji, "https://misskey.example/files/deco.png"), out[0].URL)
	require.True(t, strings.Contains(out[0].URL, mediaproxy.PathPrefix))
	require.Equal(t, -0.1, out[0].Angle)
	// Remote accounts never expose the legacy local fields.
	require.Equal(t, "", avatarDecorationURL(account))
}
