package systemsettings

// Media processing keys are database settings. LoadMediaProcessing does not
// consult the process environment, so an unsaved .env value cannot turn a
// default off or rewrite a sibling toggle.
const (
	KeyMediaImageToWebP        = "config.media_processing.image_to_webp"
	KeyMediaVideoToWebM        = "config.media_processing.video_to_webm"
	KeyMediaImageForceReencode = "config.media_processing.image_force_reencode"
	KeyMediaVideoForceReencode = "config.media_processing.video_force_reencode"
	KeyMediaStripPrivacy       = "config.media_processing.strip_privacy"
	KeyMediaImageQuality       = "config.media_processing.image_quality"
	KeyMediaVideoCRF           = "config.media_processing.video_crf"
)

const (
	DefaultImageQuality = 80
	DefaultVideoCRF     = 32
	MinImageQuality     = 40
	MaxImageQuality     = 95
	MinVideoCRF         = 20
	MaxVideoCRF         = 50
)

// MediaProcessing is a point-in-time copy of the seven admin settings.
type MediaProcessing struct {
	ImageToWebP        bool
	VideoToWebM        bool
	ImageForceReencode bool
	VideoForceReencode bool
	StripPrivacy       bool
	ImageQuality       int
	VideoCRF           int
}

// LoadMediaProcessing returns saved values, or the documented defaults when a
// key is missing or outside its range. It does not change what is stored.
func LoadMediaProcessing() MediaProcessing {
	return MediaProcessing{
		ImageToWebP:        storedBool(KeyMediaImageToWebP, true),
		VideoToWebM:        storedBool(KeyMediaVideoToWebM, true),
		ImageForceReencode: storedBool(KeyMediaImageForceReencode, true),
		VideoForceReencode: storedBool(KeyMediaVideoForceReencode, true),
		StripPrivacy:       storedBool(KeyMediaStripPrivacy, true),
		ImageQuality:       storedRange(KeyMediaImageQuality, DefaultImageQuality, MinImageQuality, MaxImageQuality),
		VideoCRF:           storedRange(KeyMediaVideoCRF, DefaultVideoCRF, MinVideoCRF, MaxVideoCRF),
	}
}

func storedBool(key string, fallback bool) bool {
	v, ok := storedPresent(key)
	if !ok {
		return fallback
	}
	return boolFrom(v, fallback)
}

func boolFrom(v any, fallback bool) bool {
	switch n := v.(type) {
	case bool:
		return n
	default:
		return fallback
	}
}

func storedRange(key string, fallback, min, max int) int {
	v, ok := storedPresent(key)
	if !ok {
		return fallback
	}
	n, ok := asInt(v)
	if !ok || n < min || n > max {
		return fallback
	}
	return n
}

func storedPresent(key string) (any, bool) {
	Load()
	mu.RLock()
	defer mu.RUnlock()
	v, ok := settings[key]
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}
