package processor

import (
	"bytes"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"

	"golang.org/x/image/draw"
)

// Processor 图片处理器接口
type Processor interface {
	// GenerateThumbnail 生成缩略图
	GenerateThumbnail(input io.Reader, opts ThumbnailOptions) (output io.Reader, width, height int, format string, err error)

	// GetDimensions 获取图片尺寸
	GetDimensions(input io.Reader) (width, height int, format string, err error)

	// IsAnimation 检查是否是动画
	IsAnimation(input io.Reader, format string) (bool, error)

	// IsSupported 是否支持该格式
	IsSupported(format string) bool
}

// DefaultProcessor 默认图片处理器
type DefaultProcessor struct {
	supportedFormats map[string]bool
}

// NewDefaultProcessor 创建默认处理器
func NewDefaultProcessor() *DefaultProcessor {
	return &DefaultProcessor{
		supportedFormats: map[string]bool{
			"jpeg": true,
			"jpg":  true,
			"png":  true,
			"gif":  true,
			"webp": true,
			"bmp":  true,
			"tiff": true,
		},
	}
}

// IsSupported 是否支持该格式
func (p *DefaultProcessor) IsSupported(format string) bool {
	format = strings.ToLower(format)
	return p.supportedFormats[format]
}

// GetDimensions 获取图片尺寸
func (p *DefaultProcessor) GetDimensions(input io.Reader) (width, height int, format string, err error) {
	// 解码图片配置（只读取头部，不加载整个图片）
	config, format, err := image.DecodeConfig(input)
	if err != nil {
		return 0, 0, "", fmt.Errorf("failed to decode image config: %w", err)
	}

	return config.Width, config.Height, format, nil
}

// IsAnimation 检查是否是动画（GIF/WebP）
func (p *DefaultProcessor) IsAnimation(input io.Reader, format string) (bool, error) {
	format = strings.ToLower(format)

	if format == "gif" {
		// 解码GIF检查帧数
		gifImg, err := gif.DecodeAll(input)
		if err != nil {
			return false, err
		}
		return len(gifImg.Image) > 1, nil
	}

	if format == "webp" {
		// WebP动画检测较复杂，这里简化处理
		// 实际项目中可以使用更专业的库
		return false, nil
	}

	return false, nil
}

// GenerateThumbnail 生成缩略图
func (p *DefaultProcessor) GenerateThumbnail(input io.Reader, opts ThumbnailOptions) (io.Reader, int, int, string, error) {
	// 解码原图
	src, _, err := image.Decode(input)
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("failed to decode image: %w", err)
	}

	// 获取原图尺寸
	bounds := src.Bounds()
	origWidth := bounds.Dx()
	origHeight := bounds.Dy()

	// 计算目标尺寸（保持比例）
	targetWidth, targetHeight := calculateDimensions(origWidth, origHeight, opts.MaxWidth, opts.MaxHeight)

	// 创建目标图片
	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))

	// 使用高质量缩放
	draw.BiLinear.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	// 编码输出
	var buf bytes.Buffer
	outputFormat := opts.Format
	if outputFormat == "" {
		outputFormat = "jpeg"
	}

	switch outputFormat {
	case "jpeg", "jpg":
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: opts.Quality})
	case "png":
		err = png.Encode(&buf, dst)
	default:
		err = jpeg.Encode(&buf, dst, &jpeg.Options{Quality: opts.Quality})
	}

	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("failed to encode image: %w", err)
	}

	return &buf, targetWidth, targetHeight, outputFormat, nil
}

// calculateDimensions 计算目标尺寸（保持比例）
func calculateDimensions(origWidth, origHeight, maxWidth, maxHeight int) (int, int) {
	if origWidth <= maxWidth && origHeight <= maxHeight {
		return origWidth, origHeight
	}

	widthRatio := float64(maxWidth) / float64(origWidth)
	heightRatio := float64(maxHeight) / float64(origHeight)

	var ratio float64
	if widthRatio < heightRatio {
		ratio = widthRatio
	} else {
		ratio = heightRatio
	}

	targetWidth := int(float64(origWidth) * ratio)
	targetHeight := int(float64(origHeight) * ratio)

	// 确保至少为1
	if targetWidth < 1 {
		targetWidth = 1
	}
	if targetHeight < 1 {
		targetHeight = 1
	}

	return targetWidth, targetHeight
}

// encodeWebP 编码为WebP格式
func encodeWebP(w io.Writer, m image.Image, quality int) error {
	// 使用golang.org/x/image/webp
	// 注意：标准库只支持解码，编码需要额外实现或使用第三方库
	// 这里简化处理，实际项目中可以使用github.com/chai2010/webp

	// 临时使用PNG作为fallback
	if quality >= 90 {
		return png.Encode(w, m)
	}

	// 否则使用JPEG
	return jpeg.Encode(w, m, &jpeg.Options{Quality: quality})
}
