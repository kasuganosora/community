package mediaproc

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/kasuganosora/community/media"
)

// ExtractFrame writes one JPEG frame. Image callers may ignore an error and
// decode the still image themselves; video callers treat it as failure.
func ExtractFrame(ctx context.Context, in, out string) error {
	cmd, err := command(ctx, "ffmpeg", append(safeIOArgs(), "-y", "-threads", "1", "-i", in, "-frames:v", "1", "-f", "image2", out))
	if err != nil {
		return err
	}
	if err = runLimited(cmd); err != nil {
		return ErrProcess
	}
	return nil
}

func defaultPreview(ctx context.Context, path, mime, work string) (string, string, int, int, error) {
	if strings.HasPrefix(mime, "audio/") {
		return "", "", 0, 0, nil
	}
	source := path
	if strings.HasPrefix(mime, "video/") || strings.HasSuffix(strings.ToLower(path), ".webp") {
		frame := filepath.Join(work, "frame.jpg")
		if err := ExtractFrame(ctx, path, frame); err != nil && strings.HasPrefix(mime, "video/") {
			return "", "", 0, 0, ErrProcess
		} else if err == nil {
			source = frame
		}
	}
	name, blur, w, h, err := media.NewImageProcessor().ProcessImage(source)
	if err != nil || name == "" {
		return "", "", 0, 0, ErrProcess
	}
	return name, blur, w, h, nil
}
