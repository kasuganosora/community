package homeserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/h2non/filetype"
	"github.com/kasuganosora/community/mediaproc"
	"github.com/kasuganosora/community/systemsettings"
)

type gatedUpload struct {
	Path string
	MIME string
	Size int64
}

// gateLocalUpload runs the shared media pipeline for a local Matrix upload.
// Remote federation copies do not call it. Non-media files stay opaque.
// A claimed image, video, or audio type whose bytes are not that media is
// rejected instead of stored.
func gateLocalUpload(ctx context.Context, path, claimed string) (gatedUpload, error) {
	detected := sniffStaged(path)
	if processableMedia(detected) {
		cfg := systemsettings.LoadMediaProcessing()
		limits := mediaproc.DefaultLimits(systemsettings.MediaImageSizeLimit(), systemsettings.MediaVideoSizeLimit())
		out, err := mediaproc.ProcessFile(ctx, path, filepath.Dir(path), detected, cfg, limits)
		if err != nil {
			return gatedUpload{}, err
		}
		return gatedUpload{Path: out.Path, MIME: out.MIME, Size: out.OutputBytes}, nil
	}
	if claimedMedia(claimed) {
		return gatedUpload{}, mediaproc.ErrCorrupt
	}
	info, err := os.Stat(path)
	if err != nil {
		return gatedUpload{}, err
	}
	mime := claimed
	if mime == "" {
		mime = "application/octet-stream"
	}
	return gatedUpload{Path: path, MIME: mime, Size: info.Size()}, nil
}

func sniffStaged(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	kind, _ := filetype.Match(buf[:n])
	mime := strings.ToLower(strings.TrimSpace(kind.MIME.Value))
	if mime == "audio/x-wav" {
		return "audio/wav"
	}
	return mime
}

func processableMedia(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp",
		"video/mp4", "video/webm", "video/quicktime",
		"audio/mpeg", "audio/ogg", "audio/wav":
		return true
	default:
		return false
	}
}

func claimedMedia(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "video/") || strings.HasPrefix(mime, "audio/")
}

func extensionForMIME(mime string) string {
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
		return ""
	}
}
