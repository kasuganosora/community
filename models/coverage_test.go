package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// media.go
// ============================================================================

func TestGetMediaType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		expected    MediaType
	}{
		{"jpeg image", "image/jpeg", MediaTypeImage},
		{"png image", "image/png", MediaTypeImage},
		{"gif image", "image/gif", MediaTypeImage},
		{"webp image", "image/webp", MediaTypeImage},
		{"mp4 video", "video/mp4", MediaTypeVideo},
		{"webm video", "video/webm", MediaTypeVideo},
		{"quicktime video", "video/quicktime", MediaTypeVideo},
		{"mpeg audio", "audio/mpeg", MediaTypeAudio},
		{"ogg audio", "audio/ogg", MediaTypeAudio},
		{"wav audio", "audio/wav", MediaTypeAudio},
		{"unknown content type", "application/pdf", MediaTypeUnknown},
		{"empty content type", "", MediaTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, GetMediaType(tt.contentType))
		})
	}
}

func TestMediaAttachment_IsImage(t *testing.T) {
	m := &MediaAttachment{Type: MediaTypeImage}
	assert.True(t, m.IsImage())

	m = &MediaAttachment{Type: MediaTypeVideo}
	assert.False(t, m.IsImage())
}

func TestMediaAttachment_IsVideo(t *testing.T) {
	m := &MediaAttachment{Type: MediaTypeVideo}
	assert.True(t, m.IsVideo())

	m = &MediaAttachment{Type: MediaTypeAudio}
	assert.False(t, m.IsVideo())
}

func TestMediaAttachment_IsAudio(t *testing.T) {
	m := &MediaAttachment{Type: MediaTypeAudio}
	assert.True(t, m.IsAudio())

	m = &MediaAttachment{Type: MediaTypeUnknown}
	assert.False(t, m.IsAudio())
}

func TestMediaAttachment_Fields(t *testing.T) {
	width := 1920
	height := 1080
	duration := 12.5
	thumb := "/thumbnails/abc.png"
	statusId := uint64(100)

	m := MediaAttachment{
		Id:            1,
		AccountId:     10,
		StatusId:      &statusId,
		FileName:      "photo.jpg",
		FilePath:      "/uploads/photo.jpg",
		FileSize:      1024,
		ContentType:   "image/jpeg",
		Type:          MediaTypeImage,
		Width:         &width,
		Height:        &height,
		Duration:      &duration,
		ThumbnailPath: &thumb,
		Description:   "a photo",
		Blurhash:      "LEHV6nWB2yk8pyo0adR*.7kCMdnj",
		FocusX:        0.5,
		FocusY:        -0.5,
	}

	assert.Equal(t, uint64(1), m.Id)
	assert.Equal(t, uint64(10), m.AccountId)
	assert.Equal(t, statusId, *m.StatusId)
	assert.Equal(t, "photo.jpg", m.FileName)
	assert.Equal(t, int64(1024), m.FileSize)
	assert.Equal(t, width, *m.Width)
	assert.Equal(t, height, *m.Height)
	assert.Equal(t, duration, *m.Duration)
	assert.Equal(t, thumb, *m.ThumbnailPath)
	assert.Equal(t, 0.5, m.FocusX)
	assert.Equal(t, -0.5, m.FocusY)
}

// ============================================================================
// matrix_user.go
// ============================================================================

func TestMatrixUser_TableName(t *testing.T) {
	m := MatrixUser{}
	assert.Equal(t, "matrix_users", m.TableName())
}

func TestMatrixUser_IsLocalUser(t *testing.T) {
	m := &MatrixUser{Direction: "local"}
	assert.True(t, m.IsLocalUser())
}

func TestMatrixUser_IsLocalUser_False(t *testing.T) {
	m := &MatrixUser{Direction: "remote"}
	assert.False(t, m.IsLocalUser())
}

func TestMatrixUser_IsRemoteUser(t *testing.T) {
	m := &MatrixUser{Direction: "remote"}
	assert.True(t, m.IsRemoteUser())
}

func TestMatrixUser_IsRemoteUser_False(t *testing.T) {
	m := &MatrixUser{Direction: "local"}
	assert.False(t, m.IsRemoteUser())
}

func TestMatrixRoom_TableName(t *testing.T) {
	m := MatrixRoom{}
	assert.Equal(t, "matrix_rooms", m.TableName())
}

func TestMatrixRoom_UpdateLastSync(t *testing.T) {
	m := &MatrixRoom{}
	assert.Nil(t, m.LastSyncAt)
	assert.Empty(t, m.LastEventID)

	m.UpdateLastSync("$event123:matrix.org")

	assert.Equal(t, "$event123:matrix.org", m.LastEventID)
	assert.NotNil(t, m.LastSyncAt)
}

func TestMatrixMessage_TableName(t *testing.T) {
	m := MatrixMessage{}
	assert.Equal(t, "matrix_messages", m.TableName())
}

func TestMatrixUser_Fields(t *testing.T) {
	m := MatrixUser{
		Id:              1,
		LocalAccountId:  100,
		MatrixUserID:    "@alice:matrix.org",
		MatrixLocalpart: "alice",
		MatrixServer:    "matrix.org",
		Direction:       "local",
		DisplayName:     "Alice",
		AvatarURL:       "mxc://matrix.org/abc",
	}
	assert.Equal(t, uint64(1), m.Id)
	assert.Equal(t, uint64(100), m.LocalAccountId)
	assert.Equal(t, "@alice:matrix.org", m.MatrixUserID)
	assert.Equal(t, "alice", m.MatrixLocalpart)
	assert.Equal(t, "matrix.org", m.MatrixServer)
	assert.Equal(t, "local", m.Direction)
	assert.Equal(t, "Alice", m.DisplayName)
	assert.Equal(t, "mxc://matrix.org/abc", m.AvatarURL)
}

func TestMatrixRoom_Fields(t *testing.T) {
	m := MatrixRoom{
		Id:                  1,
		ConversationId:      100,
		MatrixRoomID:        "!room:matrix.org",
		MatrixRoomLocalpart: "room",
		MatrixServer:        "matrix.org",
		IsDirect:            true,
		RoomName:            "Test Room",
	}
	assert.Equal(t, uint64(1), m.Id)
	assert.Equal(t, uint64(100), m.ConversationId)
	assert.Equal(t, "!room:matrix.org", m.MatrixRoomID)
	assert.Equal(t, "room", m.MatrixRoomLocalpart)
	assert.Equal(t, "matrix.org", m.MatrixServer)
	assert.True(t, m.IsDirect)
	assert.Equal(t, "Test Room", m.RoomName)
}

func TestMatrixMessage_Fields(t *testing.T) {
	m := MatrixMessage{
		Id:             1,
		StatusId:       100,
		MatrixEventID:  "$event:matrix.org",
		MatrixRoomID:   "!room:matrix.org",
		MatrixSenderID: "@bob:matrix.org",
		Direction:      "matrix->local",
	}
	assert.Equal(t, uint64(1), m.Id)
	assert.Equal(t, uint64(100), m.StatusId)
	assert.Equal(t, "$event:matrix.org", m.MatrixEventID)
	assert.Equal(t, "!room:matrix.org", m.MatrixRoomID)
	assert.Equal(t, "@bob:matrix.org", m.MatrixSenderID)
	assert.Equal(t, "matrix->local", m.Direction)
}

// ============================================================================
// thumbnail.go
// ============================================================================

func TestThumbnail_TableName(t *testing.T) {
	th := Thumbnail{}
	assert.Equal(t, "thumbnails", th.TableName())
}

func TestThumbnailJob_TableName(t *testing.T) {
	tj := ThumbnailJob{}
	assert.Equal(t, "thumbnail_jobs", tj.TableName())
}

func TestThumbnailStats_TableName(t *testing.T) {
	ts := ThumbnailStats{}
	assert.Equal(t, "thumbnail_stats", ts.TableName())
}

func TestThumbnailConfig_TableName(t *testing.T) {
	tc := ThumbnailConfig{}
	assert.Equal(t, "thumbnail_configs", tc.TableName())
}

func TestGenerateURLHash(t *testing.T) {
	hash1 := GenerateURLHash("https://example.com/image.png")
	hash2 := GenerateURLHash("https://example.com/image.png")
	hash3 := GenerateURLHash("https://example.com/different.png")

	assert.NotEmpty(t, hash1)
	assert.Equal(t, hash1, hash2, "Same URL should produce same hash")
	assert.NotEqual(t, hash1, hash3, "Different URLs should produce different hashes")
	assert.Len(t, hash1, 64, "SHA256 hex should be 64 chars")
}

func TestGenerateURLHash_EmptyString(t *testing.T) {
	hash := GenerateURLHash("")
	assert.Len(t, hash, 64)
}

func TestThumbnailStatus_Constants(t *testing.T) {
	assert.Equal(t, "pending", ThumbnailStatusPending)
	assert.Equal(t, "processing", ThumbnailStatusProcessing)
	assert.Equal(t, "completed", ThumbnailStatusCompleted)
	assert.Equal(t, "failed", ThumbnailStatusFailed)
	assert.Equal(t, "skipped", ThumbnailStatusSkipped)
}

func TestThumbnailConfig_Constants(t *testing.T) {
	assert.Equal(t, "thumbnail_enabled", ConfigKeyEnabled)
	assert.Equal(t, "thumbnail_access_mode", ConfigKeyAccessMode)
	assert.Equal(t, "thumbnail_storage_type", ConfigKeyStorageType)
}

func TestAccessMode_Constants(t *testing.T) {
	assert.Equal(t, AccessMode("proxy"), AccessModeProxy)
	assert.Equal(t, AccessMode("redirect"), AccessModeRedirect)
}

func TestStorageType_Constants(t *testing.T) {
	assert.Equal(t, StorageType("local"), StorageTypeLocal)
	assert.Equal(t, StorageType("s3"), StorageTypeS3)
	assert.Equal(t, StorageType("minio"), StorageTypeMinIO)
	assert.Equal(t, StorageType("aliyun_oss"), StorageTypeAliyunOSS)
	assert.Equal(t, StorageType("tencent_cos"), StorageTypeTencentCOS)
}

func TestThumbnail_Fields(t *testing.T) {
	th := Thumbnail{
		ID:                1,
		OriginalURL:       "https://example.com/image.png",
		OriginalURLHash:   GenerateURLHash("https://example.com/image.png"),
		OriginalSize:      1024,
		OriginalMediaType: "image",
		ThumbnailURL:      "https://example.com/thumb.png",
		ThumbnailWidth:    200,
		ThumbnailHeight:   200,
		ThumbnailFormat:   "png",
		ThumbnailSize:     512,
		StorageType:       "local",
		Bucket:            "thumbs",
		StorageKey:        "abc/thumb.png",
		Status:            ThumbnailStatusCompleted,
	}
	assert.Equal(t, uint64(1), th.ID)
	assert.Equal(t, "https://example.com/image.png", th.OriginalURL)
	assert.Equal(t, int64(1024), th.OriginalSize)
	assert.Equal(t, 200, th.ThumbnailWidth)
	assert.Equal(t, ThumbnailStatusCompleted, th.Status)
}

// ============================================================================
// user_meta.go
// ============================================================================

func TestUserMeta_TableName(t *testing.T) {
	um := UserMeta{}
	assert.Equal(t, "user_meta", um.TableName())
}

func TestUserMeta_Fields(t *testing.T) {
	um := UserMeta{
		Id:     1,
		UserId: 100,
		Key:    "theme",
		Value:  `"dark"`,
	}
	assert.Equal(t, uint64(1), um.Id)
	assert.Equal(t, uint64(100), um.UserId)
	assert.Equal(t, "theme", um.Key)
	assert.Equal(t, `"dark"`, um.Value)
}
