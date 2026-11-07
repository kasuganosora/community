package mediaproc

import "github.com/kasuganosora/community/systemsettings"

type Action string

const (
	ActionReject Action = "reject"
	ActionKeep   Action = "keep"
	ActionEncode Action = "encode"
)

const (
	ReasonConversion  = "format_conversion"
	ReasonSameFormat  = "same_format"
	ReasonPrivacy     = "privacy_rewrite"
	ReasonValidate    = "validate_only"
	ReasonUnsupported = "unsupported"
)

var (
	imageMIME = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}
	videoMIME = map[string]bool{"video/mp4": true, "video/webm": true, "video/quicktime": true}
	audioMIME = map[string]bool{"audio/mpeg": true, "audio/ogg": true, "audio/wav": true}
)

// Plan is the publish decision for one detected MIME. Decide does not read
// or write the settings store.
type Plan struct {
	Action     Action
	OutputMIME string
	Reason     string
}

// Decide applies the processing matrix. Conversion wins over force-reencode,
// which wins over a privacy-only rewrite. None of those branches change the
// stored settings.
func Decide(detectedMIME string, cfg systemsettings.MediaProcessing) Plan {
	switch {
	case imageMIME[detectedMIME]:
		return decideMedia(detectedMIME, "image/webp", cfg.ImageToWebP, cfg.ImageForceReencode, cfg.StripPrivacy)
	case videoMIME[detectedMIME]:
		return decideMedia(detectedMIME, "video/webm", cfg.VideoToWebM, cfg.VideoForceReencode, cfg.StripPrivacy)
	case audioMIME[detectedMIME]:
		return decideMedia(detectedMIME, "", false, false, cfg.StripPrivacy)
	default:
		return Plan{Action: ActionReject, Reason: ReasonUnsupported}
	}
}

func decideMedia(detected, converted string, convert, force, privacy bool) Plan {
	switch {
	case convert:
		return Plan{Action: ActionEncode, OutputMIME: converted, Reason: ReasonConversion}
	case force:
		return Plan{Action: ActionEncode, OutputMIME: detected, Reason: ReasonSameFormat}
	case privacy:
		return Plan{Action: ActionEncode, OutputMIME: detected, Reason: ReasonPrivacy}
	default:
		return Plan{Action: ActionKeep, Reason: ReasonValidate}
	}
}
