package processor

// ThumbnailOptions 缩略图选项.
type ThumbnailOptions struct {
	MaxOriginalSize   int64
	MaxWidth          int
	MaxHeight         int
	Quality           int
	Format            string
	ExcludeAnimations bool
}
