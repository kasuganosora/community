package mediaproc

import (
	"context"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/systemsettings"
)

func TestBuildEncodeArgs(t *testing.T) {
	cfg := systemsettings.MediaProcessing{ImageQuality: 80, VideoCRF: 32}
	args := BuildEncodeArgs("in.png", "out.webp", Plan{Action: ActionEncode, OutputMIME: "image/webp", Reason: ReasonConversion}, Probe{}, cfg)
	joined := slices.Concat(args)
	if !slices.Contains(args, "-nostdin") || !slices.Contains(args, "file,pipe") || !slices.Contains(args, "-threads") {
		t.Fatalf("missing safety args: %v", args)
	}
	black := ""
	if i := slices.Index(args, "-protocol_blacklist"); i < 0 || i+1 >= len(args) {
		t.Fatalf("missing protocol blacklist: %v", args)
	} else {
		black = args[i+1]
	}
	for _, proto := range []string{"http", "https", "tcp", "tls", "concat", "data", "subfile"} {
		if !strings.Contains(black, proto) {
			t.Fatalf("blacklist %q missing %s", black, proto)
		}
	}
	if i := slices.Index(args, "-protocol_whitelist"); i < 0 || args[i+1] != "file,pipe" || i > slices.Index(args, "-i") {
		t.Fatalf("whitelist must be file,pipe and precede -i: %v", args)
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
	cmd, err := command(context.Background(), "ffmpeg", []string{"-version"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cmd.Path, "prlimit") {
		t.Fatalf("limits are not applied before exec: %s", cmd.Path)
	}
	joinedCmd := strings.Join(cmd.Args, " ")
	for _, flag := range []string{"--as=", "--cpu=", "--fsize=", "--nofile=", "--"} {
		if !strings.Contains(joinedCmd, flag) {
			t.Fatalf("prlimit args missing %s: %v", flag, cmd.Args)
		}
	}
	if cmd.Env == nil || len(cmd.Env) == 0 {
		t.Fatal("child environment must not be inherited")
	}
	if !strings.Contains(joinedCmd, "/ffmpeg") && !strings.Contains(joinedCmd, "ffmpeg") {
		t.Fatalf("tool path: %v", cmd.Args)
	}
}

func TestResourceLimitsApplyBeforeExec(t *testing.T) {
	if _, err := exec.LookPath("prlimit"); err != nil {
		t.Skip("prlimit is not installed")
	}
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat is not installed")
	}
	cmd, err := command(context.Background(), "cat", []string{"/proc/self/limits"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	pending := map[string]string{
		"Max address space": "1073741824",
		"Max file size":     "536870912",
		"Max cpu time":      "120",
		"Max open files":    "64",
	}
	for _, line := range strings.Split(string(out), "\n") {
		for name, want := range pending {
			if strings.Contains(line, name) && strings.Contains(line, want) {
				delete(pending, name)
			}
		}
	}
	if len(pending) > 0 {
		t.Fatalf("limits not active before exec: missing %v\n%s", pending, out)
	}
}

func TestProbeDoesNotOpenNetworkOrExternalReferences(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is not installed")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	hit := make(chan struct{}, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			_ = conn.Close()
			hit <- struct{}{}
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := ProbeFile(ctx, "http://"+ln.Addr().String()+"/secret.jpg"); err == nil {
		t.Fatal("http input was accepted")
	}
	dir := t.TempDir()
	playlist := dir + "/remote.m3u8"
	body := "#EXTM3U\n#EXTINF:1,\nhttp://" + ln.Addr().String() + "/a.ts\n"
	if err := os.WriteFile(playlist, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ProbeFile(ctx, playlist); err == nil {
		t.Fatal("playlist input was accepted")
	}
	select {
	case <-hit:
		t.Fatal("ffprobe opened a network connection")
	case <-time.After(200 * time.Millisecond):
	}
}
