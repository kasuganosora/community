package processor

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	ffmpeg "github.com/u2takey/ffmpeg-go"
	ffprobe "gopkg.in/vansante/go-ffprobe.v2"
)

// VideoProcessor 视频处理器（生成封面图）
type VideoProcessor struct {
	ffmpegPath  string
	ffprobePath string
	seekTime    time.Duration // 提取封面时间点，默认 1 秒
}

// NewVideoProcessor 创建视频处理器
func NewVideoProcessor(ffmpegPath string) *VideoProcessor {
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	return &VideoProcessor{
		ffmpegPath:  ffmpegPath,
		ffprobePath: "ffprobe",
		seekTime:    1 * time.Second,
	}
}

// SetSeekTime 设置提取时间点
func (v *VideoProcessor) SetSeekTime(t time.Duration) {
	v.seekTime = t
}

// GenerateThumbnail 生成视频缩略图（提取第 N 秒画面）
func (v *VideoProcessor) GenerateThumbnail(input io.Reader, opts ThumbnailOptions) (io.Reader, int, int, string, error) {
	// 视频处理需要保存临时文件然后调用 FFmpeg
	tempDir := os.TempDir()
	timestamp := time.Now().UnixNano()

	// 创建临时输入文件
	tempInput := filepath.Join(tempDir, fmt.Sprintf("video_input_%d", timestamp))
	tempOutput := filepath.Join(tempDir, fmt.Sprintf("video_thumb_%d.jpg", timestamp))

	// 清理临时文件
	defer os.Remove(tempInput)
	defer os.Remove(tempOutput)

	// 写入输入文件
	inputFile, err := os.Create(tempInput)
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("failed to create temp input file: %w", err)
	}

	_, err = io.Copy(inputFile, input)
	inputFile.Close()
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("failed to write temp input file: %w", err)
	}

	// 使用 FFmpeg 提取帧
	err = v.extractFrame(tempInput, tempOutput, opts.MaxWidth, opts.MaxHeight)
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("failed to extract frame: %w", err)
	}

	// 读取生成的图片
	outputData, err := os.ReadFile(tempOutput)
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("failed to read output file: %w", err)
	}

	// 获取图片尺寸
	img, _, err := image.Decode(bytes.NewReader(outputData))
	if err != nil {
		// 如果解码失败，返回原始数据
		return bytes.NewReader(outputData), 0, 0, "jpeg", nil
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	return bytes.NewReader(outputData), width, height, "jpeg", nil
}

// extractFrame 使用 FFmpeg 提取视频帧
func (v *VideoProcessor) extractFrame(inputPath, outputPath string, maxWidth, maxHeight int) error {
	// 构建 FFmpeg 参数
	args := ffmpeg.KwArgs{
		"ss":      strconv.FormatFloat(v.seekTime.Seconds(), 'f', 3, 64),
		"vframes": 1,
		"q:v":     2, // 高质量
	}

	// 添加缩放参数
	if maxWidth > 0 && maxHeight > 0 {
		args["vf"] = fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", maxWidth, maxHeight)
	}

	err := ffmpeg.Input(inputPath, ffmpeg.KwArgs{"ss": strconv.FormatFloat(v.seekTime.Seconds(), 'f', 3, 64)}).
		Output(outputPath, args).
		OverWriteOutput().
		Run()

	if err != nil {
		// 尝试使用 exec 直接调用作为 fallback
		return v.extractFrameExec(inputPath, outputPath, maxWidth, maxHeight)
	}

	return nil
}

// extractFrameExec 使用 exec 直接调用 FFmpeg
func (v *VideoProcessor) extractFrameExec(inputPath, outputPath string, maxWidth, maxHeight int) error {
	args := []string{
		"-ss", strconv.FormatFloat(v.seekTime.Seconds(), 'f', 3, 64),
		"-i", inputPath,
		"-vframes", "1",
		"-q:v", "2",
	}

	if maxWidth > 0 && maxHeight > 0 {
		args = append(args, "-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", maxWidth, maxHeight))
	}

	args = append(args, "-y", outputPath)

	cmd := exec.Command(v.ffmpegPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg exec failed: %w, output: %s", err, string(output))
	}

	return nil
}

// GetVideoInfo 获取视频信息（时长、分辨率等）
func (v *VideoProcessor) GetVideoInfo(ctx context.Context, input io.Reader) (*VideoInfo, error) {
	// 保存到临时文件
	tempDir := os.TempDir()
	tempFile := filepath.Join(tempDir, fmt.Sprintf("video_probe_%d", time.Now().UnixNano()))
	defer os.Remove(tempFile)

	file, err := os.Create(tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	_, err = io.Copy(file, input)
	file.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}

	// 使用 ffprobe 获取信息
	probe, err := ffprobe.ProbeURL(ctx, tempFile)
	if err != nil {
		return nil, fmt.Errorf("failed to probe video: %w", err)
	}

	// Parse size and bitrate (they are strings from ffprobe)
	size, _ := strconv.ParseInt(probe.Format.Size, 10, 64)
	bitRate, _ := strconv.ParseInt(probe.Format.BitRate, 10, 64)

	info := &VideoInfo{
		Format:   probe.Format.FormatName,
		Duration: probe.Format.DurationSeconds,
		Size:     size,
		BitRate:  bitRate,
	}

	// 获取视频流信息
	if len(probe.Streams) > 0 {
		for _, stream := range probe.Streams {
			if stream.CodecType == "video" {
				info.Width = stream.Width
				info.Height = stream.Height
				info.VideoCodec = stream.CodecName
				break
			}
		}
	}

	return info, nil
}

// VideoInfo 视频信息
type VideoInfo struct {
	Format     string  `json:"format"`
	Duration   float64 `json:"duration"`
	Size       int64   `json:"size"`
	BitRate    int64   `json:"bit_rate"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	VideoCodec string  `json:"video_codec"`
}

// IsSupported 是否支持视频格式
func (v *VideoProcessor) IsSupported(format string) bool {
	supported := map[string]bool{
		"mp4":  true,
		"webm": true,
		"mov":  true,
		"avi":  true,
		"mkv":  true,
		"flv":  true,
		"wmv":  true,
	}
	return supported[format]
}

// GenerateThumbnailFromPath 从文件路径生成缩略图
func (v *VideoProcessor) GenerateThumbnailFromPath(inputPath string, opts ThumbnailOptions) (io.Reader, int, int, string, error) {
	tempDir := os.TempDir()
	tempOutput := filepath.Join(tempDir, fmt.Sprintf("video_thumb_%d.jpg", time.Now().UnixNano()))
	defer os.Remove(tempOutput)

	err := v.extractFrame(inputPath, tempOutput, opts.MaxWidth, opts.MaxHeight)
	if err != nil {
		return nil, 0, 0, "", err
	}

	outputData, err := os.ReadFile(tempOutput)
	if err != nil {
		return nil, 0, 0, "", fmt.Errorf("failed to read output file: %w", err)
	}

	// 获取图片尺寸
	img, err := jpeg.Decode(bytes.NewReader(outputData))
	if err != nil {
		// 如果解码失败，返回原始数据
		return bytes.NewReader(outputData), 0, 0, "jpeg", nil
	}

	bounds := img.Bounds()
	return bytes.NewReader(outputData), bounds.Dx(), bounds.Dy(), "jpeg", nil
}
