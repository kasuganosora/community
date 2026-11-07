package mediaproc

import (
	"testing"

	"github.com/kasuganosora/community/systemsettings"
)

func allOn() systemsettings.MediaProcessing {
	return systemsettings.MediaProcessing{ImageToWebP: true, VideoToWebM: true, ImageForceReencode: true, VideoForceReencode: true, StripPrivacy: true, ImageQuality: 80, VideoCRF: 32}
}

func TestDecide(t *testing.T) {
	on := allOn()
	cases := []struct {
		name, mime, reason, out string
		cfg                     systemsettings.MediaProcessing
		action                  Action
	}{
		{"jpeg convert", "image/jpeg", ReasonConversion, "image/webp", on, ActionEncode},
		{"webp convert still encodes", "image/webp", ReasonConversion, "image/webp", on, ActionEncode},
		{"mp4 convert", "video/mp4", ReasonConversion, "video/webm", on, ActionEncode},
		{"webm convert still encodes", "video/webm", ReasonConversion, "video/webm", on, ActionEncode},
		{"audio privacy", "audio/mpeg", ReasonPrivacy, "audio/mpeg", on, ActionEncode},
		{"jpeg same format", "image/jpeg", ReasonSameFormat, "image/jpeg", systemsettings.MediaProcessing{ImageForceReencode: true}, ActionEncode},
		{"webp privacy only", "image/webp", ReasonPrivacy, "image/webp", systemsettings.MediaProcessing{StripPrivacy: true}, ActionEncode},
		{"mp4 force", "video/mp4", ReasonSameFormat, "video/mp4", systemsettings.MediaProcessing{VideoForceReencode: true}, ActionEncode},
		{"webm keep", "video/webm", ReasonValidate, "", systemsettings.MediaProcessing{}, ActionKeep},
		{"png keep", "image/png", ReasonValidate, "", systemsettings.MediaProcessing{}, ActionKeep},
		{"wav keep", "audio/wav", ReasonValidate, "", systemsettings.MediaProcessing{}, ActionKeep},
		{"heic rejected", "image/heic", ReasonUnsupported, "", on, ActionReject},
		{"octet rejected", "application/octet-stream", ReasonUnsupported, "", on, ActionReject},
		{"quicktime convert", "video/quicktime", ReasonConversion, "video/webm", on, ActionEncode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.mime, tc.cfg)
			if got.Action != tc.action || got.OutputMIME != tc.out || got.Reason != tc.reason {
				t.Fatalf("Decide(%s) = %+v, want action %s mime %q reason %s", tc.mime, got, tc.action, tc.out, tc.reason)
			}
		})
	}
}
