package attachment

import (
	"bytes"
	"context"
	"fmt"
	"github.com/bbrks/go-blurhash"
	"github.com/h2non/filetype"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/utils"
	"github.com/pkg/errors"
	"golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	"golang.org/x/image/webp"
	"gopkg.in/vansante/go-ffprobe.v2"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type DisableAutoCreatePreviewFileOption struct{}

type AutoCreatePreviewFileOptions struct{}

type FileMetaOptions map[string]any

type DescriptionOption string

var (
	MaxPreviewWidth  = 600
	MaxPreviewHeight = 600
)

func SaveAttachmentByBytes(savePath string, fileName string, fileBytes []byte, accountId uint64, fileType uint, options ...any) (attachment *models.Attachment, err error) {
	ops := utils.AnyArrayToTypeNameMap(options)

	// 判断文件类型
	kind, _ := filetype.Match(fileBytes)
	mime := kind.MIME.Value

	if fileType == 0 {
		switch {
		case strings.Contains(mime, "image"):
			if strings.Contains(mime, "gif") {
				fileType = models.AttachmentTypeGifv
			} else {
				fileType = models.AttachmentTypeImage
			}
		case strings.Contains(mime, "video"):
			fileType = models.AttachmentTypeVideo
		case strings.Contains(mime, "audio"):
			fileType = models.AttachmentTypeAudio
		default:
			fileType = models.AttachmentTypeFile
		}
	}

	var (
		previewFileFullPath, fullSavePath string
		previewFileSize                   uint64
	)

	defer func() {
		if err == nil {
			return
		}

		if len(previewFileFullPath) > 0 {
			_ = os.Remove(previewFileFullPath)
		}

		if len(fullSavePath) > 0 {
			_ = os.Remove(fullSavePath)
		}
	}()

	blurHash, previewExtension, PreviewBytes, fileMeta, err := processFile(fileBytes, kind.Extension, fileType, ops)
	if err != nil {
		err = errors.Wrap(err, "process file error")

		return attachment, err
	}

	// 保存文件
	fullSavePath = filepath.Join(savePath, fileName)

	sourceSavePath, err := storage.Save(fullSavePath, fileBytes)
	if err != nil {
		return attachment, err
	}

	if len(PreviewBytes) > 0 {
		previewFileFullPath, err = storage.Save(filepath.Join(savePath, fileName+"."+previewExtension), PreviewBytes)
		if err != nil {
			return attachment, err
		}
	}

	// 如果有 FileMetaOptions 选项，那么就把 fileMeta 里面的内容合并进去
	if fileMetaOptions, hasSetFileMetaOptions := ops["FileMetaOptions"]; hasSetFileMetaOptions {
		fileMeta = utils.MergeMap(fileMeta, fileMetaOptions.(FileMetaOptions)) //nolint:forcetypeassert // controlled type from internal ops map
	}

	var desp string
	if descriptionOption, hasSetDescriptionOption := ops["DescriptionOption"]; hasSetDescriptionOption {
		desp = string(descriptionOption.(DescriptionOption)) //nolint:forcetypeassert
	}

	attachment = &models.Attachment{
		Type:            fileType,
		SavePath:        sourceSavePath,
		PreviewSavePath: previewFileFullPath,
		Meta:            fileMeta,
		Description:     desp,
		AccountId:       accountId,
		FileSize:        uint64(len(fileBytes)),
		PreviewFileSize: previewFileSize,
	}

	if blurHash != "" {
		attachment.BlurHash = &blurHash
	}

	err = db.GetDB().Create(attachment).Error
	if err != nil {
		err = errors.Wrap(err, "save attachment error")

		return attachment, err
	}

	return attachment, err
}

func processFile(fileBytes []byte, extension string, fileType uint, ops map[string]any) (blurHash string, previewExtension string, previewBytes []byte, fileMeta map[string]any, err error) {
	if fileType == models.AttachmentTypeImage || fileType == models.AttachmentTypeGifv {
		return processImageFile(fileBytes, extension, ops)
	}

	if fileType == models.AttachmentTypeVideo {
		return processVideoFile(fileBytes, extension, ops)
	}

	if fileType == models.AttachmentTypeAudio {
		return processAudioFile(fileBytes, extension, ops)
	}

	return "", "", nil, nil, nil
}

func processAudioFile(fileBytes []byte, extension string, ops map[string]any) (blurHash string, previewExtension string, previewBytes []byte, fileMeta map[string]any, err error) { //nolint:unparam // param reserved for future use
	fileMeta = make(map[string]any)

	ctx, cancelFn := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFn()

	data, err := ffprobe.ProbeReader(ctx, bytes.NewReader(fileBytes))
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	// 检查是否有流信息
	if len(data.Streams) == 0 {
		err = errors.New("no audio streams found")

		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	// 获取音频时长
	fileMeta["duration"] = data.Format.DurationSeconds
	// 获取音频码率
	fileMeta["bit_rate"] = data.Format.BitRate
	// 获取音频采样率
	fileMeta["sample_rate"] = data.Streams[0].SampleRate
	// 获取音频声道数
	fileMeta["channels"] = data.Streams[0].Channels
	// 获取音频格式
	fileMeta["format_name"] = data.Format.FormatName
	// 获取音频格式
	fileMeta["format_long_name"] = data.Format.FormatLongName

	// 把音频文件存到临时文件中方便后续处理
	tmpFileFileName := filepath.Join(os.TempDir(), utils.GetRandomString(32)+extension)

	err = os.WriteFile(tmpFileFileName, fileBytes, 0644) //nolint:gosec // G306: reviewed and safe
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	defer func() {
		if utils.IsFileExists(tmpFileFileName) {
			_ = os.Remove(tmpFileFileName)
		}
	}()

	// 生成音频的波形图作为预览图
	previewFileName := filepath.Join(os.TempDir(), utils.GetRandomString(32)+".png")

	// ffmpeg -i input.mp3 -filter_complex "compand,showwavespic=s=640x120" -frames:v 1 output.png
	filterStr := fmt.Sprintf("compand,showwavespic=s=%dx%d", MaxPreviewWidth, MaxPreviewHeight)

	err = runFFmpeg(tmpFileFileName, previewFileName,
		"-filter_complex", filterStr,
		"-frames:v", "1",
	)
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	previewBytes, err = os.ReadFile(previewFileName) //nolint:gosec // G304: reviewed and safe
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	img, err := getImage("png", previewBytes)
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	blurHash, err = blurhash.Encode(4, 3, img)
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	previewExtension = "png"

	return blurHash, previewExtension, previewBytes, fileMeta, err
}

func processVideoFile(fileBytes []byte, extension string, ops map[string]any) (blurHash string, previewExtension string, previewBytes []byte, fileMeta map[string]any, err error) { //nolint:unparam // param reserved for future use
	fileMeta = make(map[string]any)

	ctx, cancelFn := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFn()

	data, err := ffprobe.ProbeReader(ctx, bytes.NewReader(fileBytes))
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	// 检查是否有流信息
	if len(data.Streams) == 0 {
		err = errors.New("no video streams found")

		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	// 获取视频时长
	fileMeta["duration"] = data.Format.DurationSeconds
	// 获取视频宽度
	fileMeta["width"] = data.Streams[0].Width
	// 获取视频高度
	fileMeta["height"] = data.Streams[0].Height
	// 视频帧数
	fileMeta["nb_frames"] = data.Streams[0].NbFrames
	// 视频码率
	fileMeta["bit_rate"] = data.Format.BitRate
	// 视频编码
	fileMeta["codec_name"] = data.Streams[0].CodecName

	// 计算在总时间里第20%的时候的帧数时间，用于生成预览图
	previewTime := data.Format.DurationSeconds * 0.2
	NbFrames, _ := strconv.ParseFloat(data.Streams[0].NbFrames, 64)
	previewFrameTime := previewTime / NbFrames
	// 取得那一刻的画面
	previewFrame := int(math.Ceil(previewTime / previewFrameTime))

	// 将视频存到临时文件方便ffmpeg处理
	tmpFileFileName := filepath.Join(os.TempDir(), utils.GetRandomString(32)+"."+extension)

	err = os.WriteFile(tmpFileFileName, fileBytes, 0644) //nolint:gosec // G306: reviewed and safe
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	// 生成预览图
	previewFileName := filepath.Join(os.TempDir(), utils.GetRandomString(32)+".jpg")

	defer func() {
		if utils.IsFileExists(previewFileName) {
			_ = os.Remove(previewFileName)
		}

		if utils.IsFileExists(tmpFileFileName) {
			_ = os.Remove(tmpFileFileName)
		}
	}()

	err = runFFmpeg(tmpFileFileName, previewFileName,
		"-vf", fmt.Sprintf("select=eq(n\\,%d)", previewFrame),
		"-vframes", "1", "-q:v", "2",
	)
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	if !utils.IsFileExists(previewFileName) {
		err = errors.New("generate preview file error")

		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	previewBytes, err = os.ReadFile(previewFileName) //nolint:gosec // G304: reviewed and safe
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	img, err := getImage("jpg", previewBytes)
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	blurHash, err = blurhash.Encode(4, 3, img)
	if err != nil {
		return blurHash, previewExtension, previewBytes, fileMeta, err
	}

	previewBytes, err = createImagePreviewFile(img)

	previewExtension = "jpg"

	return blurHash, previewExtension, previewBytes, fileMeta, err
}

func processImageFile(fileBytes []byte, extension string, ops map[string]any) (blurHash string, previewExtension string, previewBytes []byte, fileMeta map[string]any, err error) { //nolint:unparam // param reserved for future use
	img, err := getImage(extension, fileBytes)
	if err != nil {
		return
	}

	// 生成blurHash
	blurHash, err = blurhash.Encode(4, 3, img)
	if err != nil {
		return
	}

	fileMeta = make(map[string]any)
	fileMeta["width"] = img.Bounds().Dx()
	fileMeta["height"] = img.Bounds().Dy()

	// 生成预览文件
	previewBytes, err = createImagePreviewFile(img)
	if err != nil {
		return
	}

	previewExtension = "jpg"

	return
}

func createImagePreviewFile(img image.Image) (previewBytes []byte, err error) {
	// 如果超过最大宽度或者最大高度，就等比例缩放
	width := img.Bounds().Dx()
	height := img.Bounds().Dy()

	// 计算缩放比例
	scale := math.Min(float64(MaxPreviewWidth)/float64(width), float64(MaxPreviewHeight)/float64(height))
	targetWidth := int(float64(width) * scale)
	targetHeight := int(float64(height) * scale)
	// 创建目标图片对象
	targetImg := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))

	// 缩放图片
	draw.CatmullRom.Scale(targetImg, targetImg.Bounds(), img, img.Bounds(), draw.Over, nil)

	// 保存图片
	buf := new(bytes.Buffer)
	// 统一保存为jpg格式
	err = jpeg.Encode(buf, targetImg, &jpeg.Options{Quality: 98})
	if err != nil {
		return
	}

	previewBytes = buf.Bytes()

	return
}

func getImage(extName string, fileBytes []byte) (ret image.Image, err error) {
	r := bytes.NewReader(fileBytes)

	switch extName {
	case "jpg", "jpeg":
		ret, err = jpeg.Decode(r)
	case "png":
		ret, err = png.Decode(r)
	case "gif":
		ret, err = gif.Decode(r)
	case "webp":
		ret, err = webp.Decode(r)
	case "bmp":
		ret, err = bmp.Decode(r)
	default:
		err = errors.New("not support image type")
	}

	return
}

var ffmpegPath string

// runFFmpeg executes ffmpeg with the given input/output and extra args.
func runFFmpeg(inputPath, outputPath string, extraArgs ...string) error {
	ffmpeg, err := FindFfmpeg()
	if err != nil {
		return err
	}

	args := []string{"-i", inputPath}
	args = append(args, extraArgs...)
	args = append(args, "-y", outputPath)

	cmd := exec.Command(ffmpeg, args...) //nolint:gosec // G204: ffmpeg path is validated
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg exec failed: %w, output: %s", err, string(output))
	}

	return nil
}

func FindFfmpeg() (path string, err error) {
	if ffmpegPath != "" {
		return ffmpegPath, nil
	}

	if runtime.GOOS != "windows" {
		path, err = exec.LookPath("ffmpeg")
		if err == nil {
			ffmpegPath = path

			return path, nil
		}
	} else {
		path, err = exec.LookPath("ffmpeg.exe")
		if err == nil {
			ffmpegPath = path

			return path, nil
		}
	}

	// 查看当前目录下有没有ffmpeg文件夹，并且里面有没有ffmpeg
	currentPath, err := os.Getwd()
	if err != nil {
		return path, err
	}

	ffmpegPath, err = checkFFmpegBinExists(currentPath)
	if err == nil {
		path = ffmpegPath

		return path, nil
	}

	// 查看这个文件所在的目录ffmpeg文件夹，并且里面有没有ffmpeg
	ffmpegPath, err = checkFFmpegBinExists(utils.GetSourceDir())
	if err == nil {
		path = ffmpegPath

		return path, nil
	}

	err = errors.New("not found ffmpeg")

	return path, err
}

func checkFFmpegBinExists(pathStr string) (fullPath string, err error) {
	if runtime.GOOS != "windows" {
		f := filepath.Join(pathStr, "ffmpeg")
		if utils.IsFileExists(f) {
			fullPath = filepath.Join(pathStr, "ffmpeg")

			return
		}
	}

	f := filepath.Join(pathStr, "ffmpeg.exe")
	if utils.IsFileExists(f) {
		fullPath = filepath.Join(pathStr, "ffmpeg")

		return
	}

	err = errors.New("not found ffmpeg")

	return
}
