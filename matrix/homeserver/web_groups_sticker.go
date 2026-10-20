package homeserver

import "strings"

const webGroupStickerMaxBytes = 512 << 10

// webGroupIsSticker detects stickers: native m.sticker events, and stickers
// that bridges (e.g. mautrix-telegram) send as m.image/m.video/m.file with
// sticker metadata, Telegram's animated sticker type, or small looping
// webm/webp media. The web UI renders them inline without a download card.
func webGroupIsSticker(eventType EventType, content map[string]any) bool {
	if _, ok := content["url"].(string); !ok {
		return false
	}
	if eventType == "m.sticker" {
		return true
	}
	if eventType != EventTypeRoomMessage {
		return false
	}
	switch webGroupString(content, "msgtype") {
	case "m.image", "m.video", "m.file":
	default:
		return false
	}
	info, _ := content["info"].(map[string]any)
	for _, m := range []map[string]any{content, info} {
		for key, value := range m {
			if strings.Contains(strings.ToLower(key), "sticker") && value != nil && value != false {
				return true
			}
		}
	}
	mime := strings.ToLower(webGroupString(info, "mimetype"))
	if mime == "application/x-tgsticker" || strings.HasSuffix(strings.ToLower(webGroupString(content, "body")), ".tgs") && mime == "application/gzip" {
		return true
	}
	if mime != "video/webm" && mime != "image/webp" {
		return false
	}
	size := webGroupMediaSize(info["size"])
	if size <= 0 || size > webGroupStickerMaxBytes {
		return false
	}
	w, h := webGroupMediaSize(info["w"]), webGroupMediaSize(info["h"])
	if w > 512 || h > 512 || w == 0 || h == 0 {
		return false
	}
	if mime == "image/webp" {
		return true
	}
	looping := info["fi.mau.loop"] == true || info["fi.mau.autoplay"] == true || info["fi.mau.gif"] == true
	return looping
}
