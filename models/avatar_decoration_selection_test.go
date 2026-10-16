package models

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAvatarDecorationSelectionsRoundTripAndClamp(t *testing.T) {
	list := AvatarDecorationSelections{AvatarDecorationSelection{ID: "3", Angle: 0.9, FlipH: true, OffsetX: -1, OffsetY: math.NaN()}.Normalized()}
	require.Equal(t, 0.5, list[0].Angle)
	require.Equal(t, -0.25, list[0].OffsetX)
	require.Equal(t, 0.0, list[0].OffsetY)
	v, err := list.Value()
	require.NoError(t, err)
	var back AvatarDecorationSelections
	require.NoError(t, back.Scan(v))
	require.Equal(t, list, back)
	require.NoError(t, back.Scan([]byte("{not json")))
	require.Nil(t, back)
	v, err = AvatarDecorationSelections(nil).Value()
	require.NoError(t, err)
	require.Nil(t, v)
}

func TestEffectiveAvatarDecorationsUpgradesLegacySelection(t *testing.T) {
	local := Account{AvatarDecorationID: 7}
	require.Equal(t, AvatarDecorationSelections{{ID: "7"}}, local.EffectiveAvatarDecorations())
	domain := "remote.example"
	remote := Account{Domain: &domain, AvatarDecorationID: 7}
	require.Nil(t, remote.EffectiveAvatarDecorations())
	local.AvatarDecorations = AvatarDecorationSelections{{ID: "8", Angle: 0.1}}
	require.Equal(t, "8", local.EffectiveAvatarDecorations()[0].ID)
}
