# media (媒体处理)

统一的媒体处理模块，负责图片的上传、缩略图生成、BlurHash 编码、图片信息提取以及响应式图片集生成。`MediaService` 同时对接本地存储与 `storage` 包的云存储后端，可同时为 Community 与 Matrix 协议提供媒体服务。

## 核心功能

- **图片处理**：解码 JPEG / PNG / GIF / WebP（通过 `image.Decode` + `golang.org/x/image/webp` 回退），提取尺寸、生成 BlurHash
- **缩略图生成**：保持宽高比的 `resize` 与居中裁剪的 `crop`（`fill`）两种方式，使用 CatmullRom 插值（质量等同 Lanczos）
- **响应式图片**：按指定宽度列表批量生成多档尺寸
- **图片优化**：对 JPEG 按指定质量重压缩
- **上传/下载**：`MediaService` 统一封装文件写入、日期分目录、图片自动处理、云存储上传
- **MXC URI**：支持 Matrix `mxc://server/mediaID` 的生成与解析
- **安全防护**：解码前校验图片像素总数（上限 1 亿像素），防御 decompression bomb 攻击

## 架构/结构

```
media/
├── processor.go   # ImageProcessor：图片解码、缩略图、BlurHash、响应式图片
├── service.go     # MediaService：上传/下载/删除/缩略图/MXC URI 统一服务
├── processor_test.go
└── service_test.go
```

模块分层：

| 层 | 组成 | 职责 |
|----|------|------|
| 图片处理 | `ImageProcessor` | 纯图片处理，不涉及 IO 路径策略 |
| 媒体服务 | `MediaService` | 组合 `ImageProcessor` + `storage.StorageProvider`，决定本地/云端落盘逻辑 |

`MediaService` 在构造时通过 `storage.GetStorage()` 获取全局存储实例，并根据 `IsEnabled()` 决定是否同步上传到云端。

## 用法示例

### 创建媒体服务并上传

```go
import "github.com/kasuganosora/community/media"

service := media.NewMediaService()
service.SetServerName("community.example.com") // 用于生成 MXC URI

// 从 multipart 表单上传（HTTP handler 中常见）
info, err := service.Upload(file, header, "image/jpeg", uploaderID, "media")
if err != nil {
    return err
}
// info.MediaID / info.FilePath / info.Width / info.Height / info.BlurHash
```

`Upload` 会自动完成：

1. 生成基于 `uuid + sha256` 的 mediaID，按 `2006/01/02` 日期分目录存储
2. 若为图片类型（`image/*`），调用 `ImageProcessor.ProcessImage` 生成缩略图与 BlurHash
3. 若启用云存储，将文件上传至云端并返回云端路径

### 下载与删除

```go
// 下载（优先云存储，回退本地文件）
reader, err := service.Download(info.FilePath)
defer reader.Close()

// 删除
if err := service.Delete(info.FilePath); err != nil {
    return err
}
```

### 生成指定尺寸缩略图

```go
// method 取值："scale"（保持比例）或 "crop"（居中裁剪填充）
data, contentType, err := service.GetThumbnail(info.FilePath, 800, 600, "scale")

// 或创建并持久化到磁盘
thumb, err := service.CreateThumbnail(info.FilePath, 800, 600, "scale")
// thumb.FilePath 形如 thumb_800x600_xxx.jpg
```

### 直接使用图片处理器

```go
proc := media.NewImageProcessor()

// 一次性生成缩略图与 BlurHash
thumbPath, blurhash, w, h, err := proc.ProcessImage("/path/to/image.jpg")

// 获取图片元信息
info, err := proc.GetImageInfo("/path/to/image.jpg")
// info.Width / info.Height / info.Format / info.BlurHash

// 批量生成响应式图片
paths, err := proc.GenerateResponsiveImages("/path/to/image.jpg", []int{320, 640, 1280})
```

### 类型判断与 MXC URI

```go
media.IsImage("image/png")  // true
media.IsVideo("video/mp4")  // true
media.IsAudio("audio/ogg")  // true

// 生成 / 解析 Matrix MXC URI
mxc := service.GetMXCURI(mediaID) // mxc://community.example.com/<mediaID>
server, id, err := media.ParseMXCURI(mxc)
```

## 核心类型与函数

### 类型

#### `ImageProcessor`

无状态图片处理器，通过 `NewImageProcessor()` 创建。

#### `ImageInfo`

图片元信息：

| 字段 | 类型 | 说明 |
|------|------|------|
| `Width` | `int` | 宽度（像素） |
| `Height` | `int` | 高度（像素） |
| `Format` | `string` | 图片格式（`jpeg` / `png` / `gif` / `webp`） |
| `BlurHash` | `string` | BlurHash 字符串 |

#### `MediaService`

媒体服务，组合 `storage.StorageProvider` + `ImageProcessor`。

#### `MediaInfo`

上传后的媒体元信息：

| 字段 | 类型 | 说明 |
|------|------|------|
| `MediaID` | `string` | 媒体唯一 ID |
| `FileName` | `string` | 存储文件名 |
| `ContentType` | `string` | MIME 类型 |
| `FileSize` | `int64` | 文件大小（字节） |
| `Width` / `Height` | `*int` | 图片宽高（仅图片有值） |
| `BlurHash` | `string` | 图片 BlurHash |
| `FilePath` | `string` | 最终存储路径（本地或云端） |
| `UploaderID` | `string` | 上传者 ID |
| `CreatedAt` | `time.Time` | 创建时间 |

#### `ThumbnailInfo`

缩略图元信息：`MediaID`、`Width`、`Height`、`ResizeMethod`（`scale` / `crop`）、`FilePath`、`CreatedAt`。

### 常量

| 常量 | 值 | 说明 |
|------|----|------|
| `ThumbnailWidth` | `400` | 默认缩略图宽度 |
| `ThumbnailHeight` | `400` | 默认缩略图最大高度 |
| `BlurHashComponentsX` | `4` | BlurHash 横向组件数 |
| `BlurHashComponentsY` | `3` | BlurHash 纵向组件数 |

### 包级函数

| 函数 | 说明 |
|------|------|
| `NewImageProcessor() *ImageProcessor` | 创建图片处理器 |
| `NewMediaService() *MediaService` | 创建媒体服务（自动接入全局存储） |
| `DetectImageType(data []byte) string` | 检测字节流的图片类型 |
| `ParseMXCURI(mxcURI string) (server, mediaID string, err error)` | 解析 MXC URI |
| `IsImage(contentType string) bool` | 判断是否为图片 |
| `IsVideo(contentType string) bool` | 判断是否为视频 |
| `IsAudio(contentType string) bool` | 判断是否为音频 |

### `ImageProcessor` 方法

| 方法 | 说明 |
|------|------|
| `ProcessImage(srcPath) (thumbPath, blurhash string, w, h int, err error)` | 处理图片：生成缩略图 + BlurHash + 尺寸 |
| `CreateThumbnail(img, srcPath, format) (string, error)` | 生成默认尺寸缩略图并落盘 |
| `CreateSquareThumbnail(img, size) image.Image` | 生成正方形缩略图（居中裁剪） |
| `GenerateBlurHash(img) (string, error)` | 生成 BlurHash |
| `GenerateResponsiveImages(srcPath, sizes []int) ([]string, error)` | 批量生成响应式图片集 |
| `OptimizeImage(srcPath, quality int) error` | 按指定质量重压缩 JPEG（原文件备份为 `.bak`） |
| `DecodeImage(path) (image.Image, string, error)` | 解码图片文件（自动回退 WebP） |
| `DecodeImageFromBytes(data) (image.Image, string, error)` | 从字节流解码图片 |
| `GetImageInfo(path) (*ImageInfo, error)` | 获取图片元信息 |
| `GetImageDimensions(path) (int, int, error)` | 获取图片宽高 |
| `IsAnimatedGIF(path) bool` | 判断是否为动图 |

### `MediaService` 方法

| 方法 | 说明 |
|------|------|
| `Upload(file, header, contentType, uploaderID, subdir) (*MediaInfo, error)` | 上传媒体，自动处理图片与云存储 |
| `UploadFromPath(srcPath, contentType, uploaderID, subdir) (*MediaInfo, error)` | 从本地路径上传 |
| `Download(filePath) (io.ReadCloser, error)` | 下载媒体 |
| `Delete(filePath) error` | 删除媒体 |
| `GetThumbnail(mediaPath, w, h, method) ([]byte, string, error)` | 实时生成缩略图（返回 JPEG 字节） |
| `CreateThumbnail(mediaPath, w, h, method) (*ThumbnailInfo, error)` | 生成并持久化缩略图 |
| `GetMXCURI(mediaID) string` | 生成 MXC URI |
| `SetServerName(name)` | 设置 Matrix 服务器名 |

## 依赖关系

| 依赖 | 用途 |
|------|------|
| `github.com/kasuganosora/community/storage` | 底层存储后端（本地 + S3/MinIO） |
| `github.com/bbrks/go-blurhash` | BlurHash 编码 |
| `golang.org/x/image/draw` | 高质量缩放（CatmullRom） |
| `golang.org/x/image/webp` | WebP 解码 |
| `github.com/google/uuid` | 媒体 ID 生成 |

> 缩略图统一编码为 JPEG（质量 85）；WebP 原图的缩略图会改写为 `.jpg` 扩展名以提升兼容性。
