package mediaproc

import (
	"context"
	"fmt"
	"os"
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

// Preview is the thumbnail used by both synchronous uploads and the queue worker.
func Preview(ctx context.Context, path, mime, work string) (string, string, int, int, error) {
	return defaultPreview(ctx, path, mime, work)
}

// BindThumbnail gives a generated thumbnail the public file's identity.
// The encoder's local name is shared (output.webp, frame.jpg), so storing
// that name unchanged would overwrite one object for every upload that day.
func BindThumbnail(dir, publicFile, generated string) (string, error) {
	name := thumbnailName(publicFile, generated)
	if sharedPreviewName(name) {
		return "", fmt.Errorf("%w: shared thumbnail name", ErrProcess)
	}
	if name == generated {
		return name, nil
	}
	from := filepath.Join(dir, generated)
	to := filepath.Join(dir, name)
	if err := os.Rename(from, to); err != nil {
		return "", err
	}
	return name, nil
}

func thumbnailName(publicFile, generated string) string {
	ext := filepath.Ext(generated)
	if ext == "" {
		ext = ".jpg"
	}
	stem := strings.TrimSuffix(filepath.Base(publicFile), filepath.Ext(publicFile))
	stem = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', 0:
			return -1
		default:
			return r
		}
	}, stem)
	if stem == "" || stem == "." || stem == ".." {
		stem = "image"
	}
	return "thumb_" + stem + ext
}

func sharedPreviewName(name string) bool {
	switch strings.ToLower(name) {
	case "thumb_frame.jpg", "thumb_frame.jpeg", "frame.jpg", "thumb_output.jpg", "thumb_output.jpeg", "thumb_output.png", "thumb_output.webp":
		return true
	default:
		return false
	}
}
