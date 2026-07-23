package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestImage 创建测试用的图片.
func createTestImage(t *testing.T, width, height int) string {
	// 创建临时目录
	tmpDir := os.TempDir()
	testPath := filepath.Join(tmpDir, "test_image.jpg")

	// 创建测试图片
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	// 填充红色
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}

	// 保存为 JPEG
	file, err := os.Create(testPath) //nolint:gosec // G304: test path
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	err = jpeg.Encode(file, img, nil)
	require.NoError(t, err)

	return testPath
}

// createTestPNG 创建测试用的 PNG 图片.
func createTestPNG(t *testing.T, width, height int) string {
	tmpDir := os.TempDir()
	testPath := filepath.Join(tmpDir, "test_image.png")

	img := image.NewRGBA(image.Rect(0, 0, width, height))

	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{R: 0, G: 255, B: 0, A: 255})
		}
	}

	file, err := os.Create(testPath) //nolint:gosec // G304: test path
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	// 使用 BestSpeed 压缩
	encoder := &png.Encoder{CompressionLevel: png.BestSpeed}
	err = encoder.Encode(file, img)
	require.NoError(t, err)

	return testPath
}

func TestNewImageProcessor(t *testing.T) {
	processor := NewImageProcessor()
	assert.NotNil(t, processor)
}

func TestProcessImage(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestImage(t, 800, 600)

	defer func() { _ = os.Remove(testPath) }()

	thumbnailPath, blurhash, width, height, err := processor.ProcessImage(testPath)

	require.NoError(t, err)
	assert.Equal(t, 800, width)
	assert.Equal(t, 600, height)
	assert.NotEmpty(t, blurhash)
	assert.NotEmpty(t, thumbnailPath)

	// 检查缩略图文件是否存在
	if thumbnailPath != "" {
		thumbnailFullPath := filepath.Join(filepath.Dir(testPath), thumbnailPath)
		defer func() { _ = os.Remove(thumbnailFullPath) }()

		_, err := os.Stat(thumbnailFullPath)
		assert.NoError(t, err, "Thumbnail file should exist")
	}
}

func TestProcessImage_PNG(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestPNG(t, 500, 400)

	defer func() { _ = os.Remove(testPath) }()

	thumbnailPath, blurhash, width, height, err := processor.ProcessImage(testPath)

	require.NoError(t, err)
	assert.Equal(t, 500, width)
	assert.Equal(t, 400, height)
	assert.NotEmpty(t, blurhash)
	assert.NotEmpty(t, thumbnailPath)

	// 清理缩略图
	if thumbnailPath != "" {
		thumbnailFullPath := filepath.Join(filepath.Dir(testPath), thumbnailPath)
		defer func() { _ = os.Remove(thumbnailFullPath) }()
	}
}

func TestProcessImage_NonExistentFile(t *testing.T) {
	processor := NewImageProcessor()

	_, _, _, _, err := processor.ProcessImage("/non/existent/file.jpg")

	require.Error(t, err)
}

func TestGenerateBlurHash(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestImage(t, 200, 150)

	defer func() { _ = os.Remove(testPath) }()

	// 读取图片
	file, err := os.Open(testPath) //nolint:gosec // G304: test path
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	img, _, err := image.Decode(file)
	require.NoError(t, err)

	// 生成 BlurHash
	hash, err := processor.GenerateBlurHash(img)

	require.NoError(t, err)
	assert.NotEmpty(t, hash)
	assert.GreaterOrEqual(t, len(hash), 6) // BlurHash has minimum length
}

func TestGenerateBlurHash_LargeImage(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestImage(t, 1000, 800)

	defer func() { _ = os.Remove(testPath) }()

	file, err := os.Open(testPath) //nolint:gosec
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	img, _, err := image.Decode(file)
	require.NoError(t, err)

	// 应该会先缩小图片再生成 hash
	hash, err := processor.GenerateBlurHash(img)

	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

func TestGetImageDimensions(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestImage(t, 640, 480)

	defer func() { _ = os.Remove(testPath) }()

	width, height, err := processor.GetImageDimensions(testPath)

	require.NoError(t, err)
	assert.Equal(t, 640, width)
	assert.Equal(t, 480, height)
}

func TestGetImageDimensions_NonExistentFile(t *testing.T) {
	processor := NewImageProcessor()

	_, _, err := processor.GetImageDimensions("/non/existent/file.jpg")

	require.Error(t, err)
}

func TestCreateThumbnail(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestImage(t, 1200, 900)

	defer func() { _ = os.Remove(testPath) }()

	// 读取图片
	file, err := os.Open(testPath) //nolint:gosec
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	img, _, err := image.Decode(file)
	require.NoError(t, err)

	// 创建缩略图
	thumbName, err := processor.CreateThumbnail(img, testPath, "jpeg")

	require.NoError(t, err)
	assert.NotEmpty(t, thumbName)

	// 清理缩略图
	thumbPath := filepath.Join(filepath.Dir(testPath), thumbName)
	defer func() { _ = os.Remove(thumbPath) }()

	// 验证缩略图存在
	_, err = os.Stat(thumbPath)
	require.NoError(t, err)
}

func TestCreateSquareThumbnail(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestImage(t, 400, 300)

	defer func() { _ = os.Remove(testPath) }()

	file, err := os.Open(testPath) //nolint:gosec
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	img, _, err := image.Decode(file)
	require.NoError(t, err)

	// 创建正方形缩略图
	squareThumb := processor.CreateSquareThumbnail(img, 100)

	assert.NotNil(t, squareThumb)
	bounds := squareThumb.Bounds()
	assert.Equal(t, 100, bounds.Dx())
	assert.Equal(t, 100, bounds.Dy())
}

func TestIsAnimatedGIF_NonAnimated(t *testing.T) {
	processor := NewImageProcessor()
	// 使用 JPEG 测试非动画
	testPath := createTestImage(t, 100, 100)
	defer func() { _ = os.Remove(testPath) }()

	// 对于非 GIF 文件，应该返回 false
	result := processor.IsAnimatedGIF(testPath)

	assert.False(t, result)
}

func TestDetectImageType_JPEG(t *testing.T) {
	testPath := createTestImage(t, 100, 100)
	defer func() { _ = os.Remove(testPath) }()

	// 读取文件内容
	data, err := os.ReadFile(testPath) //nolint:gosec
	require.NoError(t, err)

	format := DetectImageType(data)
	assert.Equal(t, "jpeg", format)
}

func TestDetectImageType_PNG(t *testing.T) {
	testPath := createTestPNG(t, 100, 100)
	defer func() { _ = os.Remove(testPath) }()

	data, err := os.ReadFile(testPath) //nolint:gosec
	require.NoError(t, err)

	format := DetectImageType(data)
	assert.Equal(t, "png", format)
}

func TestDetectImageType_Invalid(t *testing.T) {
	// 传入无效数据
	data := []byte{0x00, 0x01, 0x02, 0x03}

	format := DetectImageType(data)
	assert.Equal(t, "unknown", format)
}

func TestDecodeImage(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestImage(t, 100, 100)

	defer func() { _ = os.Remove(testPath) }()

	img, format, err := processor.DecodeImage(testPath)

	require.NoError(t, err)
	assert.NotNil(t, img)
	assert.Equal(t, "jpeg", format)
}

func TestDecodeImage_NonExistentFile(t *testing.T) {
	processor := NewImageProcessor()

	_, _, err := processor.DecodeImage("/non/existent/file.jpg")

	require.Error(t, err)
}

func TestGetImageInfo(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestImage(t, 640, 480)

	defer func() { _ = os.Remove(testPath) }()

	info, err := processor.GetImageInfo(testPath)

	require.NoError(t, err)
	assert.NotNil(t, info)
	assert.Equal(t, 640, info.Width)
	assert.Equal(t, 480, info.Height)
	assert.Equal(t, "jpeg", info.Format)
	assert.NotEmpty(t, info.BlurHash)
}

func TestGetImageInfo_NonExistentFile(t *testing.T) {
	processor := NewImageProcessor()

	_, err := processor.GetImageInfo("/non/existent/file.jpg")

	require.Error(t, err)
}

func TestOptimizeImage(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestImage(t, 100, 100)

	defer func() { _ = os.Remove(testPath) }()

	// 优化图片（重新压缩）
	err := processor.OptimizeImage(testPath, 75)

	require.NoError(t, err)

	// 验证文件仍然存在
	_, err = os.Stat(testPath)
	require.NoError(t, err)
}

func TestOptimizeImage_NonJPEG(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestPNG(t, 100, 100)

	defer func() { _ = os.Remove(testPath) }()

	// PNG 文件不会被压缩
	err := processor.OptimizeImage(testPath, 75)

	require.NoError(t, err)
}

func TestDetectImageType_Bytes(t *testing.T) {
	// 创建 JPEG 的字节
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))

	var buf bytes.Buffer

	jpeg.Encode(&buf, img, nil) //nolint:errcheck,gosec // G104: test code

	format := DetectImageType(buf.Bytes())
	assert.Equal(t, "jpeg", format)
}

// 边界测试.
func TestProcessImage_SmallImage(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestImage(t, 10, 10)

	defer func() { _ = os.Remove(testPath) }()

	thumbnailPath, blurhash, width, height, err := processor.ProcessImage(testPath)

	require.NoError(t, err)
	assert.Equal(t, 10, width)
	assert.Equal(t, 10, height)
	assert.NotEmpty(t, blurhash)

	// 清理缩略图
	if thumbnailPath != "" {
		thumbnailFullPath := filepath.Join(filepath.Dir(testPath), thumbnailPath)
		_ = os.Remove(thumbnailFullPath)
	}
}

func TestProcessImage_LargeImage(t *testing.T) {
	processor := NewImageProcessor()

	testPath := createTestImage(t, 2000, 2000)

	defer func() { _ = os.Remove(testPath) }()

	thumbnailPath, blurhash, width, height, err := processor.ProcessImage(testPath)

	require.NoError(t, err)
	assert.Equal(t, 2000, width)
	assert.Equal(t, 2000, height)
	assert.NotEmpty(t, blurhash)

	// 清理缩略图
	if thumbnailPath != "" {
		thumbnailFullPath := filepath.Join(filepath.Dir(testPath), thumbnailPath)
		_ = os.Remove(thumbnailFullPath)
	}
}
