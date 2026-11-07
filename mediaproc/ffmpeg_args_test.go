package mediaproc

import (
	"slices"
	"testing"

	"github.com/kasuganosora/community/systemsettings"
)

func TestBuildEncodeArgs(t *testing.T) {
	cfg := systemsettings.MediaProcessing{ImageQuality: 80, VideoCRF: 32}
	args := BuildEncodeArgs("in.png", "out.webp", Plan{Action: ActionEncode, OutputMIME: "image/webp", Reason: ReasonConversion}, Probe{}, cfg)
	joined := slices.Concat(args)
	if !slices.Contains(args, "-nostdin") || !slices.Contains(args, "file,pipe") || !slices.Contains(args, "-threads") {
		t.Fatalf("missing safety args: %v", args)
	}
	if i := slices.Index(args, "-threads"); i < 0 || args[i+1] != "1" {
		t.Fatalf("threads: %v", args)
	}
	if i := slices.Index(args, "-map_metadata"); i < 0 || args[i+1] != "-1" {
		t.Fatalf("metadata: %v", args)
	}
	if slices.Contains(args, "copy") {
		t.Fatalf("stream copy is forbidden: %v", args)
	}
	if slices.Contains(args, "0:a:0?") || slices.Contains(args, "0:a:0") {
		t.Fatalf("image encode mapped audio: %v", args)
	}
	if i := slices.Index(args, "-quality"); i < 0 || args[i+1] != "80" {
		t.Fatalf("quality: %v", args)
	}
	video := BuildEncodeArgs("in.mp4", "out.webm", Plan{Action: ActionEncode, OutputMIME: "video/webm"}, Probe{HasAudio: true}, cfg)
	if !slices.Contains(video, "0:v:0") || !slices.Contains(video, "0:a:0?") {
		t.Fatalf("video maps: %v", video)
	}
	if slices.Contains(video, "0:s:0") || slices.Contains(joined, "http") {
		t.Fatalf("unexpected map or protocol: %v", video)
	}
}
