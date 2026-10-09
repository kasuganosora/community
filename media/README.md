# media (媒体处理)

图片处理模块，负责缩略图生成、BlurHash 编码、图片信息提取以及响应式图片集生成。本包只处理本地（临时）文件，不负责持久化：上传流程（`controllers/api/media/store.go`、`matrix/homeserver/media.go`）先在临时目录调用 `ImageProcessor`，再通过 `storage.Default()` 把原图与缩略图写入本地 `uploads/` 或 S3。

## 核心功能

- **图片处理**：解码 JPEG / PNG / GIF / WebP（通过 `image.Decode` + `golang.org/x/image/webp` 回退），提取尺寸、生成 BlurHash
- **缩略图生成**：保持宽高比的 `resize` 与居中裁剪的 `crop`（`fill`）两种方式，使用 CatmullRom 插值（质量等同 Lanczos）
- **响应式图片**：按指定宽度列表批量生成多档尺寸
- **图片优化**：对 JPEG 按指定质量重压缩
- **安全防护**：解码前校验图片像素总数（上限 1 亿像素），防御 decompression bomb 攻击

## 架构/结构

```
media/
├── processor.go   # ImageProcessor：图片解码、缩略图、BlurHash、响应式图片
└── processor_test.go
```

## 用法示例

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
| `DetectImageType(data []byte) string` | 检测字节流的图片类型 |

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

## 依赖关系

| 依赖 | 用途 |
|------|------|
| `github.com/bbrks/go-blurhash` | BlurHash 编码 |
| `golang.org/x/image/draw` | 高质量缩放（CatmullRom） |
| `golang.org/x/image/webp` | WebP 解码 |

> 缩略图统一编码为 JPEG（质量 85）；WebP 原图的缩略图会改写为 `.jpg` 扩展名以提升兼容性。
