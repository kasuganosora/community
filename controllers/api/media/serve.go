package media

import (
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// mediaRootDir is where Upload/UploadAsync store files (relative to the
// working directory, see upload.go).
var mediaRootDir = filepath.Join("uploads", "media")

// Serve handles GET /media/:id/*file, the URLs produced by
// serializer.ToMediaAttachment:
//
//	/media/<id>/<file_name>
//	/media/<id>/thumbnail/<thumbnail_path>
//
// Previously no route served these URLs, so every uploaded image/video URL
// fell through to the SPA index.html.
func Serve(c *gin.Context) {
	requested := strings.TrimPrefix(c.Param("file"), "/")

	var media models.MediaAttachment
	if err := db.GetDB().Where("id = ?", c.Param("id")).First(&media).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})

		return
	}

	// media.FilePath is "<yyyy>/<mm>/<dd>/<file_name>"; the thumbnail lives
	// next to it. Only these two exact names are served, so the request
	// path can never be used to reach other files.
	dir := path.Dir(filepath.ToSlash(media.FilePath))

	var (
		rel         string
		contentType string
	)

	switch {
	case media.FileName != "" && media.FileName != "processing" && requested == media.FileName:
		rel = path.Join(dir, media.FileName)
		contentType = media.ContentType
	case media.ThumbnailPath != nil && *media.ThumbnailPath != "" && requested == "thumbnail/"+*media.ThumbnailPath:
		rel = path.Join(dir, *media.ThumbnailPath)
		contentType = "" // Sniff bytes below, never trust an imported thumbnail extension.
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})

		return
	}

	if strings.Contains(rel, "..") {
		c.JSON(http.StatusNotFound, gin.H{"error": "Not found"})

		return
	}

	if contentType == "" {
		file, err := os.Open(filepath.Join(mediaRootDir, filepath.FromSlash(rel)))
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		sample := make([]byte, 512)
		n, _ := file.Read(sample)
		_ = file.Close()
		contentType = http.DetectContentType(sample[:n])
	}
	parsed, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		parsed = "application/octet-stream"
	}
	// Setting a specific inert MIME plus nosniff prevents attacker-controlled
	// metadata/extensions from turning downloaded HTML into an active document.
	c.Header("Content-Type", parsed)
	if !safeInlineMediaType(parsed) {
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(rel)}))
	}

	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.File(filepath.Join(mediaRootDir, filepath.FromSlash(rel)))
}

func safeInlineMediaType(contentType string) bool {
	switch strings.ToLower(contentType) {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/avif",
		"audio/mpeg", "audio/mp4", "audio/aac", "audio/ogg", "audio/webm", "audio/wav", "audio/x-wav", "audio/flac", "audio/x-flac",
		"video/mp4", "video/webm", "video/ogg", "video/quicktime", "video/mpeg":
		return true
	}
	return false
}
