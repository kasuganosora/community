package media //nolint:goconst // acceptable string literal usage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- IsImage ----------

func TestIsImage(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{"JPEG", "image/jpeg", true}, //nolint:goconst
		{"PNG", "image/png", true},
		{"GIF", "image/gif", true},
		{"WebP", "image/webp", true},
		{"SVG", "image/svg+xml", true},
		{"Lowercase prefix", "image/something", true},
		{"Video", "video/mp4", false},  //nolint:goconst
		{"Audio", "audio/mpeg", false}, //nolint:goconst
		{"Empty", "", false},           //nolint:goconst
		{"Plain text", "text/plain", false},
		{"Application", "application/pdf", false}, //nolint:goconst
		{"Image uppercase prefix", "Image/jpeg", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsImage(tt.contentType))
		})
	}
}

// ---------- IsVideo ----------

func TestIsVideo(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{"MP4", "video/mp4", true},
		{"WebM", "video/webm", true},
		{"AVI", "video/avi", true},
		{"Image", "image/jpeg", false},
		{"Audio", "audio/mpeg", false},
		{"Empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsVideo(tt.contentType))
		})
	}
}

// ---------- IsAudio ----------

func TestIsAudio(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{"MP3", "audio/mpeg", true},
		{"OGG", "audio/ogg", true},
		{"WAV", "audio/wav", true},
		{"Image", "image/jpeg", false},
		{"Video", "video/mp4", false},
		{"Empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, IsAudio(tt.contentType))
		})
	}
}

// ---------- GetMediaType ----------

func TestGetMediaType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        string
	}{
		{"JPEG", "image/jpeg", "image"}, //nolint:goconst
		{"PNG", "image/png", "image"},
		{"MP4", "video/mp4", "video"}, //nolint:goconst
		{"WebM", "video/webm", "video"},
		{"MP3", "audio/mpeg", "audio"}, //nolint:goconst
		{"OGG", "audio/ogg", "audio"},
		{"Text", "text/plain", "unknown"}, //nolint:goconst
		{"PDF", "application/pdf", "unknown"},
		{"Empty", "", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, GetMediaType(tt.contentType))
		})
	}
}

// ---------- ParseMXCURI ----------

func TestParseMXCURI(t *testing.T) {
	server, mediaID, err := ParseMXCURI("mxc://example.com/abc123")
	require.NoError(t, err)
	assert.Equal(t, "example.com", server)
	assert.Equal(t, "abc123", mediaID)
}

func TestParseMXCURI_WithComplexID(t *testing.T) {
	server, mediaID, err := ParseMXCURI("mxc://matrix.org/some_long_media_id_with_underscores")
	require.NoError(t, err)
	assert.Equal(t, "matrix.org", server)
	assert.Equal(t, "some_long_media_id_with_underscores", mediaID)
}

func TestParseMXCURI_EmptyString(t *testing.T) {
	_, _, err := ParseMXCURI("")
	require.Error(t, err)
}

func TestParseMXCURI_InvalidPrefix(t *testing.T) {
	_, _, err := ParseMXCURI("http://example.com/abc")
	require.Error(t, err)
}

func TestParseMXCURI_MissingSlash(t *testing.T) {
	_, _, err := ParseMXCURI("mxc://example.com")
	require.Error(t, err)
}

func TestParseMXCURI_EmptyMediaID(t *testing.T) {
	// "mxc://server/" — after trim, split gives ["server", ""] which is accepted by format
	server, mediaID, err := ParseMXCURI("mxc://server/")
	require.NoError(t, err)
	assert.Equal(t, "server", server)
	assert.Empty(t, mediaID)
}

func TestParseMXCURI_TooManySlashes(t *testing.T) {
	_, _, err := ParseMXCURI("mxc://server/media/extra")
	require.Error(t, err)
}

func TestParseMXCURI_NoServer(t *testing.T) {
	_, _, err := ParseMXCURI("mxc://")
	require.Error(t, err)
}

// ---------- GetMXCURI ----------

func TestGetMXCURI_EmptyServerName(t *testing.T) {
	s := &MediaService{}
	uri := s.GetMXCURI("media_abc")
	assert.Equal(t, "mxc://localhost/media_abc", uri)
}

func TestGetMXCURI_WithServerName(t *testing.T) {
	s := &MediaService{serverName: "example.com"}
	uri := s.GetMXCURI("media_xyz")
	assert.Equal(t, "mxc://example.com/media_xyz", uri)
}

func TestGetMXCURI_WithServerNameSet(t *testing.T) {
	s := &MediaService{}
	s.SetServerName("matrix.org")
	uri := s.GetMXCURI("test123")
	assert.Equal(t, "mxc://matrix.org/test123", uri)
}

// ---------- SetServerName ----------

func TestSetServerName(t *testing.T) {
	s := &MediaService{}
	assert.Empty(t, s.serverName)

	s.SetServerName("my.server.org")
	assert.Equal(t, "my.server.org", s.serverName)

	// Overwrite
	s.SetServerName("another.server")
	assert.Equal(t, "another.server", s.serverName)
}

// ---------- generateMediaID ----------

func TestGenerateMediaID(t *testing.T) {
	s := &MediaService{}
	id := s.generateMediaID()

	assert.NotEmpty(t, id)
	// Should be base64url-encoded 16 bytes → 22 characters
	assert.Len(t, id, 22)
	// No padding
	assert.NotContains(t, id, "=")
}

func TestGenerateMediaID_Uniqueness(t *testing.T) {
	s := &MediaService{}
	ids := make(map[string]bool)

	for range 100 {
		id := s.generateMediaID()
		assert.False(t, ids[id], "generated duplicate ID: %s", id)
		ids[id] = true
	}

	assert.Len(t, ids, 100)
}

// ---------- NewMediaService ----------

func TestNewMediaService(t *testing.T) {
	svc := NewMediaService()
	assert.NotNil(t, svc)
	assert.NotNil(t, svc.imageProc)
	assert.Equal(t, "uploads", svc.uploadDir)
}

// ---------- MediaInfo struct creation ----------

func TestMediaInfo_StructCreation(t *testing.T) {
	w, h := 100, 200
	info := &MediaInfo{
		MediaID:     "test_id",
		FileName:    "photo.jpg",
		ContentType: "image/jpeg",
		FileSize:    1024,
		Width:       &w,
		Height:      &h,
		BlurHash:    "LKO2:N%2Tw=w]~RBVZRi};RPxuwH",
		FilePath:    "/uploads/photo.jpg",
		UploaderID:  "user_42",
		CreatedAt:   time.Now(),
	}

	assert.Equal(t, "test_id", info.MediaID)
	assert.Equal(t, "photo.jpg", info.FileName)
	assert.Equal(t, "image/jpeg", info.ContentType)
	assert.Equal(t, int64(1024), info.FileSize)
	assert.NotNil(t, info.Width)
	assert.Equal(t, 100, *info.Width)
	assert.NotNil(t, info.Height)
	assert.Equal(t, 200, *info.Height)
	assert.Equal(t, "LKO2:N%2Tw=w]~RBVZRi};RPxuwH", info.BlurHash)
	assert.Equal(t, "/uploads/photo.jpg", info.FilePath)
	assert.Equal(t, "user_42", info.UploaderID)
	assert.False(t, info.CreatedAt.IsZero())
}

func TestMediaInfo_NilDimensions(t *testing.T) {
	info := &MediaInfo{
		MediaID:     "doc_id",
		ContentType: "application/pdf",
	}

	assert.Nil(t, info.Width)
	assert.Nil(t, info.Height)
	assert.Empty(t, info.BlurHash)
}

// ---------- ThumbnailInfo struct creation ----------

func TestThumbnailInfo_StructCreation(t *testing.T) {
	info := &ThumbnailInfo{
		MediaID:      "thumb_1",
		Width:        150,
		Height:       150,
		ResizeMethod: "crop",
		FilePath:     "/uploads/thumb_150x150_photo.jpg",
		CreatedAt:    time.Now(),
	}

	assert.Equal(t, "thumb_1", info.MediaID)
	assert.Equal(t, 150, info.Width)
	assert.Equal(t, 150, info.Height)
	assert.Equal(t, "crop", info.ResizeMethod)
	assert.Equal(t, "/uploads/thumb_150x150_photo.jpg", info.FilePath)
	assert.False(t, info.CreatedAt.IsZero())
}

func TestThumbnailInfo_ZeroValues(t *testing.T) {
	info := &ThumbnailInfo{}

	assert.Empty(t, info.MediaID)
	assert.Equal(t, 0, info.Width)
	assert.Equal(t, 0, info.Height)
	assert.Empty(t, info.ResizeMethod)
	assert.Empty(t, info.FilePath)
	assert.True(t, info.CreatedAt.IsZero())
}

// ---------- MediaService struct creation ----------

func TestMediaService_StructCreation(t *testing.T) {
	svc := &MediaService{
		uploadDir:  "custom_uploads",
		useCloud:   false,
		serverName: "test.local",
	}

	assert.NotNil(t, svc)
	assert.Equal(t, "custom_uploads", svc.uploadDir)
	assert.False(t, svc.useCloud)
	assert.Equal(t, "test.local", svc.serverName)
}
