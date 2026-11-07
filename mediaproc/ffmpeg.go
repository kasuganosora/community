package mediaproc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/h2non/filetype"
	"github.com/kasuganosora/community/metrics"
	"github.com/kasuganosora/community/systemsettings"
	_ "golang.org/x/image/webp"
)

// A child FFmpeg process is not a sandbox. prlimit sets CPU, address-space,
// file-size and fd caps and then execs ffmpeg or ffprobe, so the limits exist
// before those programs parse input. There is still no seccomp, user
// namespace, or network namespace. The child environment is cleared so a
// proxy variable cannot be inherited. Protocol whitelist is file and pipe;
// network and external-reference protocols are blacklisted.

const (
	imageTimeout = 60 * time.Second
	cpuSeconds   = 120
	addressLimit = 1 << 30
	fileLimit    = 512 << 20
	fdLimit      = 64
)

var (
	ErrCorrupt  = errors.New("invalid or corrupt media")
	ErrPixels   = errors.New("media exceeds the pixel limit")
	ErrDuration = errors.New("media exceeds the duration limit")
	ErrRate     = errors.New("media exceeds the frame rate limit")
	ErrProcess  = errors.New("media processing failed")
	ErrTooLarge = errors.New("file size too large")
)

// Limits are the non-optional resource checks.
type Limits struct {
	MaxImagePixels int
	MaxVideoPixels int
	MaxFrameRate   float64
	MaxDuration    float64
	MaxImageBytes  int64
	MaxVideoBytes  int64
}

// DefaultLimits matches the documented constants and the existing media caps.
func DefaultLimits(imageBytes, videoBytes int64) Limits {
	pixels := systemsettings.GetInt(systemsettings.KeyMediaImageMatrixLimit, 16*1024*1024)
	if pixels <= 0 || pixels > 32*1024*1024 {
		pixels = 16 * 1024 * 1024
	}
	videoPixels := systemsettings.GetInt(systemsettings.KeyMediaVideoMatrixLimit, 8294400)
	if videoPixels <= 0 {
		videoPixels = 8294400
	}
	rate := systemsettings.GetInt(systemsettings.KeyMediaVideoFrameRate, 120)
	if rate <= 0 {
		rate = 120
	}
	return Limits{MaxImagePixels: pixels, MaxVideoPixels: videoPixels, MaxFrameRate: float64(rate), MaxDuration: 300, MaxImageBytes: imageBytes, MaxVideoBytes: videoBytes}
}

// Probe is container metadata from ffprobe. Tags are not logged.
type Probe struct {
	Width, Height int
	PixFmt        string
	Duration      float64
	FrameRate     float64
	HasAudio      bool
	HasAlpha      bool
	Animated      bool
	Tags          map[string]string
}

type Output struct {
	Path         string
	MIME         string
	Info         Probe
	KeptOriginal bool
	InputBytes   int64
	OutputBytes  int64
}

// ProcessFile validates or re-encodes one staged file. workDir receives the
// generated file. The original path is returned only when the plan is keep.
func ProcessFile(ctx context.Context, inputPath, workDir string, detected string, cfg systemsettings.MediaProcessing, limits Limits) (out Output, err error) {
	started := time.Now()
	info, statErr := os.Stat(inputPath)
	kind := kindOf(detected)
	defer func() {
		result := "ok"
		if err != nil {
			result = "rejected"
		}
		var in, written int64
		if info != nil {
			in = info.Size()
		}
		written = out.OutputBytes
		metrics.RecordMediaProcessing(kind, result, in, written, time.Since(started))
	}()
	if statErr != nil {
		return Output{}, ErrCorrupt
	}
	plan := Decide(detected, cfg)
	if plan.Action == ActionReject {
		return Output{}, errors.New("unsupported media type")
	}
	probe, err := ProbeFile(ctx, inputPath)
	if err != nil {
		return Output{}, ErrCorrupt
	}
	if plan.Action == ActionEncode && probe.Tags["png_default_excluded"] == "1" {
		// FFmpeg publishes the PNG default image and drops the animation.
		return Output{}, ErrProcess
	}
	if err = enforceLimits(kind, info.Size(), probe, limits); err != nil {
		return Output{}, err
	}
	if plan.Action == ActionKeep {
		if err = DecodeValidate(ctx, inputPath); err != nil {
			return Output{}, ErrCorrupt
		}
		return Output{Path: inputPath, MIME: detected, Info: probe, KeptOriginal: true, InputBytes: info.Size(), OutputBytes: info.Size()}, nil
	}
	ext := extensionFor(plan.OutputMIME)
	dest := filepath.Join(workDir, "output"+ext)
	if err = Encode(ctx, inputPath, dest, plan, probe, cfg, nil); err != nil {
		return Output{}, err
	}
	checked, err := validateOutput(ctx, dest, plan.OutputMIME, probe, limits, kind)
	if err != nil {
		return Output{}, err
	}
	n, _ := os.Stat(dest)
	size := int64(0)
	if n != nil {
		size = n.Size()
	}
	capBytes := limits.MaxVideoBytes
	if kind == "image" {
		capBytes = limits.MaxImageBytes
	}
	if capBytes > 0 && size > capBytes {
		return Output{}, ErrTooLarge
	}
	return Output{Path: dest, MIME: plan.OutputMIME, Info: checked, InputBytes: info.Size(), OutputBytes: size}, nil
}

func kindOf(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return "image"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	default:
		return "other"
	}
}

func enforceLimits(kind string, size int64, probe Probe, limits Limits) error {
	pixels := int64(probe.Width) * int64(probe.Height)
	switch kind {
	case "image":
		if limits.MaxImageBytes > 0 && size > limits.MaxImageBytes {
			return ErrTooLarge
		}
		if probe.Width > 0 && pixels > int64(limits.MaxImagePixels) {
			return ErrPixels
		}
	case "video":
		if limits.MaxVideoBytes > 0 && size > limits.MaxVideoBytes {
			return ErrTooLarge
		}
		if probe.Width > 0 && pixels > int64(limits.MaxVideoPixels) {
			return ErrPixels
		}
		if probe.Duration > limits.MaxDuration {
			return ErrDuration
		}
		if probe.FrameRate > limits.MaxFrameRate {
			return ErrRate
		}
	default:
		if limits.MaxVideoBytes > 0 && size > limits.MaxVideoBytes {
			return ErrTooLarge
		}
	}
	return nil
}

func extensionFor(mime string) string {
	switch mime {
	case "image/webp":
		return ".webp"
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "video/webm":
		return ".webm"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "audio/mpeg":
		return ".mp3"
	case "audio/ogg":
		return ".ogg"
	case "audio/wav":
		return ".wav"
	default:
		return ".bin"
	}
}

// ProbeFile reads container metadata. The JSON is not logged.
func ProbeFile(ctx context.Context, path string) (Probe, error) {
	ctx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()
	cmd, err := command(ctx, "ffprobe", append(probeIOArgs(), "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path))
	if err != nil {
		return Probe{}, err
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = io.Discard
	if err := runLimited(cmd); err != nil {
		return Probe{}, ErrCorrupt
	}
	var raw struct {
		Streams []struct {
			CodecType string            `json:"codec_type"`
			CodecName string            `json:"codec_name"`
			Width     int               `json:"width"`
			Height    int               `json:"height"`
			PixFmt    string            `json:"pix_fmt"`
			Duration  string            `json:"duration"`
			NbFrames  string            `json:"nb_frames"`
			Rate      string            `json:"avg_frame_rate"`
			Tags      map[string]string `json:"tags"`
		} `json:"streams"`
		Format struct {
			Duration string            `json:"duration"`
			Tags     map[string]string `json:"tags"`
		} `json:"format"`
	}
	if json.Unmarshal(buf.Bytes(), &raw) != nil {
		return Probe{}, ErrCorrupt
	}
	var probe Probe
	probe.Tags = map[string]string{}
	for k, v := range raw.Format.Tags {
		probe.Tags[k] = v
	}
	probe.Duration = parseFloat(raw.Format.Duration)
	for _, s := range raw.Streams {
		for k, v := range s.Tags {
			probe.Tags[k] = v
		}
		if s.CodecType == "audio" {
			probe.HasAudio = true
			if probe.Duration == 0 {
				probe.Duration = parseFloat(s.Duration)
			}
			continue
		}
		if s.CodecType != "video" {
			continue
		}
		probe.Width, probe.Height = s.Width, s.Height
		probe.PixFmt = s.PixFmt
		probe.FrameRate = parseRate(s.Rate)
		if d := parseFloat(s.Duration); d > probe.Duration {
			probe.Duration = d
		}
		// The GIF decoder always reports bgra, including when the palette has no
		// transparent index. That is not evidence of an alpha channel.
		if pixHasAlpha(s.PixFmt) && s.CodecName != "gif" {
			probe.HasAlpha = true
		}
		if parseFloat(s.NbFrames) > 1 {
			probe.Animated = true
		}
	}
	annotateWebP(path, &probe)
	annotatePNG(path, &probe)
	// A pixel format that can store alpha is not transparency. Opaque frames
	// may be encoded without an alpha plane.
	if probe.HasAlpha && imageFullyOpaque(path) {
		probe.HasAlpha = false
	}
	return probe, nil
}

func annotatePNG(path string, probe *Probe) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 8 || string(data[:8]) != "\x89PNG\r\n\x1a\n" {
		return
	}
	sawIDATBeforeFrame := false
	sawFrame := false
	for offset := 8; offset+12 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[offset:]))
		if n < 0 || offset+12+n > len(data) {
			return
		}
		kind := string(data[offset+4 : offset+8])
		chunk := data[offset+8 : offset+8+n]
		if kind == "acTL" && len(chunk) >= 4 && binary.BigEndian.Uint32(chunk) > 1 {
			probe.Animated = true
		}
		if kind == "IDAT" && !sawFrame {
			sawIDATBeforeFrame = true
		}
		if kind == "fcTL" {
			if sawIDATBeforeFrame {
				probe.Tags["png_default_excluded"] = "1"
			}
			sawFrame = true
		}
		offset += 12 + n
	}
}

func imageFullyOpaque(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	img, _, err := image.Decode(f)
	if err != nil {
		return false
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || b.Dx()*b.Dy() > 1_000_000 {
		return false
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := img.At(x, y).RGBA()
			if a != 0xffff {
				return false
			}
		}
	}
	return true
}

// annotateWebP records animation, alpha, and canvas size from the RIFF
// container. This ffmpeg build can write animated WebP but its own decoder
// reports those files as having no pixel format.
func annotateWebP(path string, probe *Probe) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) < 21 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return
	}
	frames := 0
	for offset := 12; offset+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[offset+4:]))
		if size < 0 || offset+8+size > len(data) {
			return
		}
		kind := string(data[offset : offset+4])
		chunk := data[offset+8 : offset+8+size]
		if kind == "VP8X" && len(chunk) >= 10 {
			if chunk[0]&2 != 0 {
				probe.Animated = true
			}
			// libwebp sets bit 4 (0x10) when a frame carries alpha.
			if chunk[0]&0x10 != 0 {
				probe.HasAlpha = true
			}
			if probe.Width == 0 {
				probe.Width = 1 + int(chunk[4]) | int(chunk[5])<<8 | int(chunk[6])<<16
				probe.Height = 1 + int(chunk[7]) | int(chunk[8])<<8 | int(chunk[9])<<16
			}
		}
		if kind == "ANMF" {
			frames++
		}
		offset += 8 + size + (size & 1)
	}
	if frames > 1 {
		probe.Animated = true
	}
}

func parseFloat(s string) float64 {
	n, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return n
}

func parseRate(s string) float64 {
	if s == "" || s == "0/0" {
		return 0
	}
	left, right, ok := strings.Cut(s, "/")
	if !ok {
		return parseFloat(s)
	}
	den := parseFloat(right)
	if den == 0 {
		return 0
	}
	return parseFloat(left) / den
}

// DecodeValidate fully decodes the file and writes nothing.
func DecodeValidate(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, imageTimeout)
	defer cancel()
	cmd, err := command(ctx, "ffmpeg", append(safeIOArgs(), "-v", "error", "-threads", "1", "-i", path, "-f", "null", "-"))
	if err != nil {
		return err
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := runLimited(cmd); err != nil {
		return ErrCorrupt
	}
	return nil
}

// Encode re-encodes in to out. onProgress receives 0–100 from FFmpeg's clock.
func Encode(ctx context.Context, in, out string, plan Plan, probe Probe, cfg systemsettings.MediaProcessing, onProgress func(int)) error {
	timeout := imageTimeout
	if strings.HasPrefix(plan.OutputMIME, "video/") {
		timeout = 3 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := BuildEncodeArgs(in, out, plan, probe, cfg)
	cmd, err := command(ctx, "ffmpeg", args)
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ErrProcess
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return ErrProcess
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	scanProgress(stdout, probe.Duration, onProgress)
	select {
	case err = <-done:
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-done
		return ErrProcess
	}
	if err != nil {
		return ErrProcess
	}
	if onProgress != nil {
		onProgress(100)
	}
	return nil
}

// BuildEncodeArgs returns the ffmpeg argument vector. It never uses a shell
// and never copies the input stream.
func BuildEncodeArgs(in, out string, plan Plan, probe Probe, cfg systemsettings.MediaProcessing) []string {
	args := append(safeIOArgs(), "-y", "-threads", "1", "-i", in, "-map_metadata", "-1", "-fflags", "+bitexact")
	video := strings.HasPrefix(plan.OutputMIME, "video/") || strings.HasPrefix(plan.OutputMIME, "image/")
	if video && !strings.HasPrefix(plan.OutputMIME, "audio/") {
		args = append(args, "-map", "0:v:0")
	}
	if strings.HasPrefix(plan.OutputMIME, "video/") {
		args = append(args, "-map", "0:a:0?")
	}
	if strings.HasPrefix(plan.OutputMIME, "audio/") {
		args = append(args, "-map", "0:a:0")
	}
	switch plan.OutputMIME {
	case "image/webp":
		pix := "yuv420p"
		if probe.HasAlpha {
			pix = "yuva420p"
		}
		args = append(args, "-vf", "scale='max(2,trunc((iw+1)/2)*2)':'max(2,trunc((ih+1)/2)*2)',format="+pix, "-c:v", "libwebp", "-quality", strconv.Itoa(cfg.ImageQuality), "-pix_fmt", pix)
		if probe.Animated {
			args = append(args, "-loop", "0")
		} else {
			args = append(args, "-frames:v", "1")
		}
		args = append(args, "-an", "-f", "webp")
	case "image/jpeg":
		args = append(args, "-c:v", "mjpeg", "-q:v", strconv.Itoa(jpegQScale(cfg.ImageQuality)), "-pix_fmt", "yuvj420p", "-frames:v", "1", "-an", "-f", "image2", "-update", "1")
	case "image/png":
		args = append(args, "-c:v", "png", "-pix_fmt", "rgba", "-frames:v", "1", "-an", "-f", "image2", "-update", "1")
	case "image/gif":
		args = append(args, "-c:v", "gif", "-an", "-f", "gif")
	case "video/webm":
		pix := "yuv420p"
		if probe.HasAlpha {
			pix = "yuva420p"
		}
		args = append(args, "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2", "-c:v", "libvpx-vp9", "-crf", strconv.Itoa(cfg.VideoCRF), "-b:v", "0", "-cpu-used", "4", "-deadline", "good", "-row-mt", "1", "-pix_fmt", pix)
		if probe.HasAudio {
			args = append(args, "-c:a", "libopus", "-b:a", "64k")
		} else {
			args = append(args, "-an")
		}
		args = append(args, "-f", "webm")
	case "video/mp4", "video/quicktime":
		format := "mp4"
		if plan.OutputMIME == "video/quicktime" {
			format = "mov"
		}
		args = append(args, "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2", "-c:v", "libx264", "-crf", strconv.Itoa(cfg.VideoCRF), "-pix_fmt", "yuv420p", "-movflags", "+faststart")
		if probe.HasAudio {
			args = append(args, "-c:a", "aac", "-b:a", "96k")
		} else {
			args = append(args, "-an")
		}
		args = append(args, "-f", format)
	case "audio/mpeg":
		args = append(args, "-c:a", "libmp3lame", "-q:a", "4", "-vn", "-f", "mp3")
	case "audio/ogg":
		args = append(args, "-c:a", "libvorbis", "-q:a", "5", "-vn", "-f", "ogg")
	case "audio/wav":
		args = append(args, "-c:a", "pcm_s16le", "-vn", "-f", "wav")
	}
	args = append(args, "-flags", "+bitexact", out)
	return args
}

func jpegQScale(quality int) int {
	if quality < systemsettings.MinImageQuality {
		quality = systemsettings.MinImageQuality
	}
	if quality > systemsettings.MaxImageQuality {
		quality = systemsettings.MaxImageQuality
	}
	// mjpeg qscale: 2 is best, 31 is worst. 95 -> 2, 40 -> 20.
	return 2 + (systemsettings.MaxImageQuality-quality)*18/(systemsettings.MaxImageQuality-systemsettings.MinImageQuality)
}

func validateOutput(ctx context.Context, path, mime string, input Probe, limits Limits, kind string) (Probe, error) {
	f, err := os.Open(path)
	if err != nil {
		return Probe{}, ErrProcess
	}
	head := make([]byte, 8192)
	n, _ := io.ReadFull(f, head)
	_ = f.Close()
	kindMatch, _ := filetype.Match(head[:n])
	got := strings.ToLower(kindMatch.MIME.Value)
	if got == "audio/x-wav" {
		got = "audio/wav"
	}
	if got != mime && !(mime == "video/quicktime" && (got == "video/quicktime" || got == "video/mp4")) {
		return Probe{}, fmt.Errorf("%w: magic %s", ErrProcess, got)
	}
	probe, err := ProbeFile(ctx, path)
	if err != nil {
		return Probe{}, fmt.Errorf("%w: probe", ErrProcess)
	}
	if err = enforceLimits(kind, 0, probe, limits); err != nil {
		return Probe{}, err
	}
	if rejectTags(probe.Tags) {
		return Probe{}, fmt.Errorf("%w: metadata", ErrProcess)
	}
	if input.HasAlpha && strings.HasPrefix(mime, "image/") && mime != "image/jpeg" && mime != "image/gif" && !probe.HasAlpha {
		return Probe{}, fmt.Errorf("%w: alpha pix=%s", ErrProcess, probe.PixFmt)
	}
	if input.Animated && (mime == "image/webp" || mime == "image/gif") && !probe.Animated {
		return Probe{}, fmt.Errorf("%w: animation", ErrProcess)
	}
	if input.HasAudio && strings.HasPrefix(mime, "video/") && !probe.HasAudio {
		return Probe{}, fmt.Errorf("%w: audio", ErrProcess)
	}
	return probe, nil
}

func rejectTags(tags map[string]string) bool {
	for key, value := range tags {
		if !allowedMetadata(key, value) {
			return true
		}
	}
	return false
}

func allowedMetadata(key, value string) bool {
	name := tagName(key)
	switch name {
	case "handler_name", "vendor_id", "major_brand", "compatible_brands":
		return true
	case "encoder":
		return knownEncoder(value)
	case "duration":
		// The muxer writes this timing field. It is not a free-form comment.
		return durationValue(value)
	default:
		return false
	}
}

func durationValue(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t") {
		return false
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return true
	}
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return false
	}
	for i, part := range parts {
		if _, err := strconv.ParseFloat(part, 64); err != nil {
			return false
		}
		if i < 2 && strings.Contains(part, ".") {
			return false
		}
	}
	return true
}

func tagName(key string) string {
	name := strings.ToLower(strings.TrimSpace(key))
	return strings.TrimSuffix(name, "-eng")
}

func pixHasAlpha(pix string) bool {
	switch pix {
	case "rgba", "bgra", "argb", "abgr", "ya8", "ya16le", "gbrap", "gbrapf32le", "yuva420p", "yuva422p", "yuva444p":
		return true
	default:
		return strings.HasPrefix(pix, "yuva") || strings.HasPrefix(pix, "rgba") || strings.HasPrefix(pix, "gbrap")
	}
}

func knownEncoder(value string) bool {
	v := strings.TrimSpace(value)
	return strings.HasPrefix(v, "Lavf") || strings.HasPrefix(v, "Lavc")
}

// probeIOArgs omits -nostdin: ffprobe treats that flag as taking a value.
func probeIOArgs() []string {
	return append([]string{"-hide_banner"}, protocolArgs()...)
}

// safeIOArgs is placed before -i. file and pipe are local. The blacklist
// drops network protocols and demuxers that can open external references
// even if a later flag widens the whitelist.
func safeIOArgs() []string {
	return append([]string{"-hide_banner", "-nostdin"}, protocolArgs()...)
}

func protocolArgs() []string {
	return []string{
		"-protocol_whitelist", "file,pipe",
		"-protocol_blacklist", "concat,subfile,data,crypto,http,https,tls,tcp,udp,ftp,rtmp,rtsp,gopher,srtp,rtp,mmsh,mmst,srt,ipfs,httpproxy",
	}
}

func command(ctx context.Context, bin string, args []string) (*exec.Cmd, error) {
	prlimit, err := exec.LookPath("prlimit")
	if err != nil {
		return nil, ErrProcess
	}
	tool, err := exec.LookPath(bin)
	if err != nil {
		return nil, ErrProcess
	}
	// prlimit applies the caps and then execs the absolute tool path, so
	// parsing cannot start before the limits exist and PATH is not required.
	wrapped := []string{
		"--as=" + strconv.FormatUint(addressLimit, 10),
		"--cpu=" + strconv.Itoa(cpuSeconds),
		"--fsize=" + strconv.FormatUint(fileLimit, 10),
		"--nofile=" + strconv.Itoa(fdLimit),
		"--", tool,
	}
	wrapped = append(wrapped, args...)
	cmd := exec.CommandContext(ctx, prlimit, wrapped...)
	cmd.Env = []string{"LANG=C", "LC_ALL=C"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return cmd.Process.Kill()
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd, nil
}

func runLimited(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Wait()
}

func scanProgress(r io.Reader, duration float64, onProgress func(int)) {
	s := bufio.NewScanner(r)
	var last int
	for s.Scan() {
		line := s.Text()
		if onProgress == nil {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if key == "progress" && val == "end" {
			onProgress(100)
			continue
		}
		if key != "out_time_us" && key != "out_time_ms" {
			continue
		}
		n, err := strconv.ParseFloat(val, 64)
		if err != nil || duration <= 0 {
			continue
		}
		// Both keys are microseconds in current FFmpeg progress output.
		percent := int(n / 1e6 / duration * 100)
		if percent < 0 {
			percent = 0
		}
		if percent > 99 {
			percent = 99
		}
		if percent != last {
			last = percent
			onProgress(percent)
		}
	}
}
