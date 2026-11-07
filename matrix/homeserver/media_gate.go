package homeserver

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/h2non/filetype"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/mediaproc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// processLocal is the encode used for a local Matrix upload. Tests replace it.
var processLocal = mediaproc.ProcessFile

type gatedUpload struct {
	Path string
	MIME string
	Size int64
}

// gateLocalUpload runs the shared media pipeline for a local Matrix upload.
// Remote federation copies do not call it. Non-media files stay opaque.
// A claimed image, video, or audio type whose bytes are not that media is
// rejected instead of stored. Video reserves the same durable queue slot as
// an API upload before ffmpeg starts, then releases it when this call returns.
func gateLocalUpload(ctx context.Context, path, claimed, uploader string) (gated gatedUpload, err error) {
	detected := sniffStaged(path)
	if processableMedia(detected) {
		encodeCtx := ctx
		if strings.HasPrefix(detected, "video/") {
			owner, ownerErr := localVideoOwner(uploader)
			if ownerErr != nil {
				return gatedUpload{}, ownerErr
			}
			info, statErr := os.Stat(path)
			if statErr != nil {
				return gatedUpload{}, statErr
			}
			hold, holdErr := mediaproc.HoldVideo(ctx, db.GetDB(), owner, path, info.Size())
			if holdErr != nil {
				return gatedUpload{}, holdErr
			}
			defer func() {
				state := models.ProcessReady
				if err != nil {
					state = models.ProcessFailed
					if ctx.Err() != nil {
						state = models.ProcessCancelled
					}
				}
				_ = hold.Release(context.WithoutCancel(ctx), db.GetDB(), state)
			}()
			var stop func()
			encodeCtx, stop = hold.Keep(ctx, db.GetDB())
			defer stop()
		}
		cfg := systemsettings.LoadMediaProcessing()
		limits := mediaproc.DefaultLimits(systemsettings.MediaImageSizeLimit(), systemsettings.MediaVideoSizeLimit())
		out, encodeErr := processLocal(encodeCtx, path, filepath.Dir(path), detected, cfg, limits)
		if encodeErr != nil {
			return gatedUpload{}, encodeErr
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

// localVideoOwner maps the authenticated Matrix user to the local account
// that owns the video quota. An empty id, a remote MXID, or a bridge actor
// does not get a slot and does not share account 0.
func localVideoOwner(uploader string) (uint64, error) {
	localpart, _, ok := ParseMatrixUserID(strings.TrimSpace(uploader))
	if !ok || localpart == "" || models.IsReservedBridgeUsername(localpart) {
		return 0, errors.New("uploader is not a local account")
	}
	var account models.Account
	if err := db.GetDB().Where("username = ?", localpart).First(&account).Error; err != nil || account.Id == 0 {
		return 0, errors.New("uploader is not a local account")
	}
	return account.Id, nil
}

func rejectMatrixAdmission(c *gin.Context, err error) bool {
	if errors.Is(err, mediaproc.ErrQueue) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"errcode": "M_LIMIT_EXCEEDED", "error": err.Error()})
		return true
	}
	return false
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
