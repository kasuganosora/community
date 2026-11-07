package systemsettings

import (
	"testing"

	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/require"
)

func TestMediaProcessingDefaultsAndRanges(t *testing.T) {
	loaded = true
	mu.Lock()
	for _, key := range []string{KeyMediaImageToWebP, KeyMediaVideoToWebM, KeyMediaImageForceReencode, KeyMediaVideoForceReencode, KeyMediaStripPrivacy, KeyMediaImageQuality, KeyMediaVideoCRF} {
		delete(settings, key)
	}
	mu.Unlock()
	previous, had := config.C.Other[KeyMediaImageToWebP]
	if config.C.Other == nil {
		config.C.Other = map[string]string{}
	}
	config.C.Other[KeyMediaImageToWebP] = "false"
	t.Cleanup(func() {
		if had {
			config.C.Other[KeyMediaImageToWebP] = previous
		} else {
			delete(config.C.Other, KeyMediaImageToWebP)
		}
	})

	got := LoadMediaProcessing()
	require.True(t, got.ImageToWebP, "an environment value must not override an unsaved processing default")
	require.True(t, got.VideoToWebM)
	require.True(t, got.ImageForceReencode)
	require.True(t, got.VideoForceReencode)
	require.True(t, got.StripPrivacy)
	require.Equal(t, 80, got.ImageQuality)
	require.Equal(t, 32, got.VideoCRF)

	Set(KeyMediaImageToWebP, false)
	Set(KeyMediaImageQuality, 101)
	t.Cleanup(func() {
		Unset(KeyMediaImageToWebP)
		Unset(KeyMediaImageQuality)
	})
	got = LoadMediaProcessing()
	require.False(t, got.ImageToWebP)
	require.True(t, got.VideoToWebM, "saving the image toggle must not change the video toggle")
	require.Equal(t, 80, got.ImageQuality, "out-of-range stored quality falls back to the default")
}
