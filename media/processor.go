package media

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbrks/go-blurhash"
	"github.com/kasuganosora/community/log"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

const (
	// ThumbnailWidth 缩略图宽度
	ThumbnailWidth = 400
	// ThumbnailHeight 缩略图最大高度
	ThumbnailHeight = 400
	// BlurHashComponentsX BlurHash X 组件数
	BlurHashComponentsX = 4
	// BlurHashComponentsY BlurHash Y 组件数
	BlurHashComponentsY = 3
)

// ImageProcessor 图片处理器
type ImageProcessor struct{}

// NewImageProcessor 创建图片处理器
func NewImageProcessor() *ImageProcessor {
	return &ImageProcessor{}
}

// resizeImage 调整图片大小（保持宽高比）
// 使用 CatmullRom 插值（等同于 Lanczos 质量）
func resizeImage(src image.Image, width, height int) image.Image {
	if width <= 0 && height <= 0 {
		return src
	}

	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()

	// 计算目标尺寸（保持宽高比）
	if width <= 0 {
		ratio := float64(height) / float64(srcH)
		width = int(float64(srcW) * ratio)
	} else if height <= 0 {
		ratio := float64(width) / float64(srcW)
		height = int(float64(srcH) * ratio)
	}

	// 创建目标图像
	dst := image.NewRGBA(image.Rect(0, 0, width, height))

	// 使用 CatmullRom 缩放（等同于 Lanczos 质量）
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	return dst
}

// fillImage 裁剪填充到指定尺寸（居中裁剪）
func fillImage(src image.Image, width, height int) image.Image {
	bounds := src.Bounds()
	srcW, srcH := bounds.Dx(), bounds.Dy()

	// 计算缩放比例（覆盖目标区域）
	scaleX := float64(width) / float64(srcW)
	scaleY := float64(height) / float64(srcH)
	scale := scaleX
	if scaleY > scaleX {
		scale = scaleY
	}

	// 先缩放
	newW := int(float64(srcW) * scale)
	newH := int(float64(srcH) * scale)
	scaled := resizeImage(src, newW, newH)

	// 居中裁剪
	offsetX := (newW - width) / 2
	offsetY := (newH - height) / 2

	// 创建裁剪后的图像
	result := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(result, result.Bounds(), scaled, image.Point{X: offsetX, Y: offsetY}, draw.Src)

	return result
}

// ProcessImage 处理图片：生成缩略图和 BlurHash
func (p *ImageProcessor) ProcessImage(srcPath string) (thumbnailPath string, blurhash string, width int, height int, err error) {
	// 打开图片文件
	file, err := os.Open(srcPath)
	if err != nil {
		return "", "", 0, 0, err
	}
	defer file.Close()

	// 解码图片
	img, format, err := image.Decode(file)
	if err != nil {
		return "", "", 0, 0, err
	}

	// 获取图片尺寸
	bounds := img.Bounds()
	width = bounds.Dx()
	height = bounds.Dy()

	// 生成 BlurHash
	blurhash, err = p.GenerateBlurHash(img)
	if err != nil {
		log.Warn("Failed to generate blurhash: %v", err)
		// 不返回错误，继续处理
	}

	// 生成缩略图
	thumbnailPath, err = p.CreateThumbnail(img, srcPath, format)
	if err != nil {
		log.Warn("Failed to create thumbnail: %v", err)
		// 不返回错误，继续处理
	}

	return thumbnailPath, blurhash, width, height, nil
}

// CreateThumbnail 创建缩略图
func (p *ImageProcessor) CreateThumbnail(img image.Image, srcPath string, format string) (string, error) {
	// 计算缩略图尺寸
	thumbImg := resizeImage(img, ThumbnailWidth, 0)

	// 生成缩略图路径
	ext := filepath.Ext(srcPath)
	dir := filepath.Dir(srcPath)
	base := filepath.Base(srcPath)
	thumbName := "thumb_" + base
	thumbPath := filepath.Join(dir, thumbName)

	// 根据格式保存
	var err error
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		err = p.saveJPEG(thumbImg, thumbPath)
	case "png":
		err = p.savePNG(thumbImg, thumbPath)
	case "gif":
		err = p.saveGIF(thumbImg, thumbPath)
	case "webp":
		// WebP 保存为 JPEG 格式以兼容性更好
		err = p.saveJPEG(thumbImg, thumbPath)
		// 更改扩展名
		newThumbPath := strings.TrimSuffix(thumbPath, ext) + ".jpg"
		if err == nil {
			os.Rename(thumbPath, newThumbPath)
			thumbPath = newThumbPath
		}
	default:
		// 默认保存为 JPEG
		err = p.saveJPEG(thumbImg, thumbPath)
	}

	if err != nil {
		return "", err
	}

	return thumbName, nil
}

// saveJPEG 保存为 JPEG
func (p *ImageProcessor) saveJPEG(img image.Image, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	options := &jpeg.Options{Quality: 85}
	return jpeg.Encode(file, img, options)
}

// savePNG 保存为 PNG
func (p *ImageProcessor) savePNG(img image.Image, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := &png.Encoder{CompressionLevel: png.BestSpeed}
	return encoder.Encode(file, img)
}

// saveGIF 保存为 GIF
func (p *ImageProcessor) saveGIF(img image.Image, path string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	return gif.Encode(file, img, nil)
}

// GenerateBlurHash 生成 BlurHash
func (p *ImageProcessor) GenerateBlurHash(img image.Image) (string, error) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// 如果图片太大，先缩小以提高性能
	maxSize := 100
	if width > maxSize || height > maxSize {
		scale := float64(maxSize) / float64(width)
		if float64(height)*scale > float64(maxSize) {
			scale = float64(maxSize) / float64(height)
		}
		newWidth := int(float64(width) * scale)
		newHeight := int(float64(height) * scale)
		img = resizeImage(img, newWidth, newHeight)
		bounds = img.Bounds()
		width = bounds.Dx()
		height = bounds.Dy()
	}

	// 转换为 RGBA
	rgba := image.NewRGBA(bounds)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			rgba.Set(x, y, img.At(x, y))
		}
	}

	// 生成 BlurHash
	hash, err := blurhash.Encode(BlurHashComponentsX, BlurHashComponentsY, rgba)
	if err != nil {
		return "", err
	}

	return hash, nil
}

// GetImageDimensions 获取图片尺寸
func (p *ImageProcessor) GetImageDimensions(path string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}

	return config.Width, config.Height, nil
}

// DecodeImage 解码图片（支持多种格式）
func (p *ImageProcessor) DecodeImage(path string) (image.Image, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()

	// 尝试解码
	img, format, err := image.Decode(file)
	if err != nil {
		// 如果标准解码失败，尝试 WebP
		file.Seek(0, 0)
		img, err = webp.Decode(file)
		if err != nil {
			return nil, "", err
		}
		format = "webp"
	}

	return img, format, nil
}

// DecodeImageFromBytes 从字节数组解码图片
func (p *ImageProcessor) DecodeImageFromBytes(data []byte) (image.Image, string, error) {
	reader := bytes.NewReader(data)

	// 尝试解码
	img, format, err := image.Decode(reader)
	if err != nil {
		// 如果标准解码失败，尝试 WebP
		reader.Seek(0, 0)
		img, err = webp.Decode(reader)
		if err != nil {
			return nil, "", err
		}
		format = "webp"
	}

	return img, format, nil
}

// IsAnimatedGIF 检查是否为动图
func (p *ImageProcessor) IsAnimatedGIF(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	// 读取 GIF
	gifImg, err := gif.DecodeAll(file)
	if err != nil {
		return false
	}

	return len(gifImg.Image) > 1
}

// OptimizeImage 优化图片（压缩）
func (p *ImageProcessor) OptimizeImage(srcPath string, quality int) error {
	file, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer file.Close()

	img, format, err := image.Decode(file)
	if err != nil {
		return err
	}

	// 如果是 JPEG，重新压缩
	if format == "jpeg" || format == "jpg" {
		backupPath := srcPath + ".bak"
		os.Rename(srcPath, backupPath)
		defer os.Remove(backupPath)

		return p.saveJPEGWithQuality(img, srcPath, quality)
	}

	return nil
}

// saveJPEGWithQuality 以指定质量保存 JPEG
func (p *ImageProcessor) saveJPEGWithQuality(img image.Image, path string, quality int) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	options := &jpeg.Options{Quality: quality}
	return jpeg.Encode(file, img, options)
}

// GenerateResponsiveImages 生成响应式图片集
func (p *ImageProcessor) GenerateResponsiveImages(srcPath string, sizes []int) ([]string, error) {
	file, err := os.Open(srcPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(srcPath)
	base := strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))

	var paths []string
	for _, size := range sizes {
		resized := resizeImage(img, size, 0)
		path := filepath.Join(dir, fmt.Sprintf("%s_%d.jpg", base, size))

		if err := p.saveJPEG(resized, path); err != nil {
			return nil, err
		}

		paths = append(paths, path)
	}

	return paths, nil
}

// CreateSquareThumbnail 创建正方形缩略图
func (p *ImageProcessor) CreateSquareThumbnail(img image.Image, size int) image.Image {
	return fillImage(img, size, size)
}

// ReadImageInfo 读取图片信息
type ImageInfo struct {
	Width    int
	Height   int
	Format   string
	BlurHash string
}

// GetImageInfo 获取图片信息
func (p *ImageProcessor) GetImageInfo(path string) (*ImageInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	config, format, err := image.DecodeConfig(file)
	if err != nil {
		return nil, err
	}

	info := &ImageInfo{
		Width:  config.Width,
		Height: config.Height,
		Format: format,
	}

	// 生成 BlurHash
	file.Seek(0, 0)
	img, _, err := image.Decode(file)
	if err == nil {
		info.BlurHash, _ = p.GenerateBlurHash(img)
	}

	return info, nil
}

// DetectImageType 检测图片类型
func DetectImageType(data []byte) string {
	reader := bytes.NewReader(data)
	_, format, err := image.DecodeConfig(reader)
	if err != nil {
		return "unknown"
	}
	return format
}
