package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/storage"
)

// MediaService 公共媒体服务
// 统一处理 Community 和 Matrix 的媒体上传/下载
type MediaService struct {
	storage    storage.StorageProvider
	imageProc  *ImageProcessor
	uploadDir  string
	useCloud   bool
	serverName string // 用于生成 MXC URI
}

// MediaInfo 媒体信息
type MediaInfo struct {
	MediaID     string
	FileName    string
	ContentType string
	FileSize    int64
	Width       *int
	Height      *int
	BlurHash    string
	FilePath    string
	UploaderID  string
	CreatedAt   time.Time
}

// ThumbnailInfo 缩略图信息
type ThumbnailInfo struct {
	MediaID      string
	Width        int
	Height       int
	ResizeMethod string
	FilePath     string
	CreatedAt    time.Time
}

// NewMediaService 创建媒体服务
func NewMediaService() *MediaService {
	cloudStorage := storage.GetStorage()
	return &MediaService{
		storage:   cloudStorage,
		imageProc: NewImageProcessor(),
		uploadDir: "uploads",
		useCloud:  cloudStorage.IsEnabled(),
	}
}

// SetServerName 设置服务器名称（用于 MXC URI）
func (s *MediaService) SetServerName(name string) {
	s.serverName = name
}

// Upload 上传媒体文件
func (s *MediaService) Upload(file io.Reader, header *multipart.FileHeader, contentType, uploaderID, subdir string) (*MediaInfo, error) {
	// 生成媒体 ID
	mediaID := s.generateMediaID()

	// 获取文件扩展名
	ext := ""
	if header != nil {
		ext = filepath.Ext(header.Filename)
	} else {
		// 从 Content-Type 推断扩展名
		exts, _ := mime.ExtensionsByType(contentType)
		if len(exts) > 0 {
			ext = exts[0]
		}
	}

	// 确定存储路径
	datePath := time.Now().Format("2006/01/02")
	fileName := mediaID + ext
	relativePath := filepath.Join(subdir, datePath, fileName)
	fullPath := filepath.Join(s.uploadDir, relativePath)

	// 创建目录
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// 创建目标文件
	dst, err := os.Create(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}
	defer func() { _ = dst.Close() }()

	// 复制文件内容
	fileSize, err := io.Copy(dst, file)
	if err != nil {
		return nil, fmt.Errorf("failed to save file: %w", err)
	}

	// 获取文件信息
	var width, height *int
	var blurHash string

	// 处理图片
	if strings.HasPrefix(contentType, "image/") {
		thumbPath, bh, w, h, err := s.imageProc.ProcessImage(fullPath)
		if err == nil {
			width = &w
			height = &h
			blurHash = bh
			// 缩略图路径记录（可用于后续上传到云存储）
			_ = filepath.Join(filepath.Dir(fullPath), thumbPath)
		}
	}

	// 上传到云存储（如果启用）
	cloudPath := ""
	if s.useCloud {
		cp, err := s.storage.Upload(fullPath, subdir+"/"+relativePath)
		if err == nil {
			cloudPath = cp
		}
	}

	// 确定最终路径
	finalPath := fullPath
	if cloudPath != "" {
		finalPath = cloudPath
	}

	return &MediaInfo{
		MediaID:     mediaID,
		FileName:    fileName,
		ContentType: contentType,
		FileSize:    fileSize,
		Width:       width,
		Height:      height,
		BlurHash:    blurHash,
		FilePath:    finalPath,
		UploaderID:  uploaderID,
		CreatedAt:   time.Now(),
	}, nil
}

// UploadFromPath 从本地路径上传
func (s *MediaService) UploadFromPath(srcPath, contentType, uploaderID, subdir string) (*MediaInfo, error) {
	file, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fileInfo, _ := file.Stat()
	header := &multipart.FileHeader{
		Filename: fileInfo.Name(),
		Size:     fileInfo.Size(),
	}

	return s.Upload(file, header, contentType, uploaderID, subdir)
}

// Download 下载媒体文件
func (s *MediaService) Download(filePath string) (io.ReadCloser, error) {
	// 尝试从云存储获取
	if s.useCloud {
		reader, err := s.storage.Download(filePath)
		if err == nil {
			return reader, nil
		}
	}

	// 从本地存储读取
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	return file, nil
}

// Delete 删除媒体文件
func (s *MediaService) Delete(filePath string) error {
	if s.useCloud {
		if err := s.storage.Delete(filePath); err == nil {
			return nil
		}
	}
	return os.Remove(filePath)
}

// GetThumbnail 获取缩略图
func (s *MediaService) GetThumbnail(mediaPath string, width, height int, method string) ([]byte, string, error) {
	// 获取原图
	reader, err := s.Download(mediaPath)
	if err != nil {
		return nil, "", err
	}
	defer reader.Close()

	// 读取全部内容以支持多次解码尝试
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read image data: %w", err)
	}

	// 解码图片
	img, _, err := s.imageProc.DecodeImageFromBytes(data)
	if err != nil {
		return nil, "", err
	}

	// 生成缩略图
	var thumbImg image.Image
	if method == "scale" {
		thumbImg = resizeImage(img, width, height)
	} else {
		thumbImg = fillImage(img, width, height)
	}

	// 编码为 JPEG
	buf := &bytes.Buffer{}
	if err := jpeg.Encode(buf, thumbImg, &jpeg.Options{Quality: 85}); err != nil {
		return nil, "", err
	}

	return buf.Bytes(), "image/jpeg", nil
}

// CreateThumbnail 创建并保存缩略图
func (s *MediaService) CreateThumbnail(mediaPath string, width, height int, method string) (*ThumbnailInfo, error) {
	thumbData, _, err := s.GetThumbnail(mediaPath, width, height, method)
	if err != nil {
		return nil, err
	}

	// 生成缩略图路径
	dir := filepath.Dir(mediaPath)
	base := filepath.Base(mediaPath)
	ext := filepath.Ext(base)
	thumbName := fmt.Sprintf("thumb_%dx%d_%s", width, height, base)
	if ext != ".jpg" && ext != ".jpeg" {
		thumbName = strings.TrimSuffix(thumbName, ext) + ".jpg"
	}
	thumbPath := filepath.Join(dir, thumbName)

	// 保存缩略图
	if err := os.WriteFile(thumbPath, thumbData, 0644); err != nil {
		return nil, err
	}

	return &ThumbnailInfo{
		MediaID:      "",
		Width:        width,
		Height:       height,
		ResizeMethod: method,
		FilePath:     thumbPath,
		CreatedAt:    time.Now(),
	}, nil
}

// generateMediaID 生成媒体 ID
func (s *MediaService) generateMediaID() string {
	data := fmt.Sprintf("%s%d", uuid.New().String(), time.Now().UnixNano())
	hash := sha256.Sum256([]byte(data))
	return base64.RawURLEncoding.EncodeToString(hash[:16])
}

// GetMXCURI 获取 MXC URI
func (s *MediaService) GetMXCURI(mediaID string) string {
	if s.serverName == "" {
		return fmt.Sprintf("mxc://localhost/%s", mediaID)
	}
	return fmt.Sprintf("mxc://%s/%s", s.serverName, mediaID)
}

// ParseMXCURI 解析 MXC URI
func ParseMXCURI(mxcURI string) (serverName, mediaID string, err error) {
	if !strings.HasPrefix(mxcURI, "mxc://") {
		return "", "", fmt.Errorf("invalid MXC URI")
	}

	parts := strings.Split(strings.TrimPrefix(mxcURI, "mxc://"), "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid MXC URI format")
	}

	return parts[0], parts[1], nil
}

// IsImage 判断是否为图片类型
func IsImage(contentType string) bool {
	return strings.HasPrefix(contentType, "image/")
}

// IsVideo 判断是否为视频类型
func IsVideo(contentType string) bool {
	return strings.HasPrefix(contentType, "video/")
}

// IsAudio 判断是否为音频类型
func IsAudio(contentType string) bool {
	return strings.HasPrefix(contentType, "audio/")
}

// GetMediaType 获取媒体类型
func GetMediaType(contentType string) string {
	if IsImage(contentType) {
		return "image"
	}
	if IsVideo(contentType) {
		return "video"
	}
	if IsAudio(contentType) {
		return "audio"
	}
	return "unknown"
}
