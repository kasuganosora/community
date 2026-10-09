package serializer

import (
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"strings"
)

type Emoji struct {
	Shortcode       string   `json:"shortcode"`
	Url             string   `json:"url"`
	StaticUrl       string   `json:"static_url"`
	VisibleInPicker bool     `json:"visible_in_picker"`
	Category        *string  `json:"category"`
	Aliases         []string `json:"aliases"`
	License         string   `json:"license"`
	LocalOnly       bool     `json:"local_only"`
	IsSensitive     bool     `json:"is_sensitive"`
}

func ToEmoji(emoji *models.Emoji) *Emoji {
	if emoji == nil {
		return nil
	}
	emojiURL := mediaproxy.URL(mediaproxy.KindEmoji, absoluteURL(emoji.Url))

	staticURL := absoluteURL(emoji.StaticUrl)
	if staticURL == "" {
		staticURL = absoluteURL(emoji.Url)
	}

	var category *string
	if emoji.Category != "" {
		category = &emoji.Category
	}

	return &Emoji{
		Shortcode:       emoji.Shortcode,
		Url:             emojiURL,
		StaticUrl:       mediaproxy.URL(mediaproxy.KindEmoji, staticURL),
		VisibleInPicker: emoji.VisibleInPicker,
		Category:        category,
		Aliases:         append([]string{}, emoji.Aliases...), License: emoji.License, LocalOnly: emoji.LocalOnly, IsSensitive: emoji.IsSensitive,
	}
}

func ToEmojis(emojis []*models.Emoji) []*Emoji {
	var res = make([]*Emoji, 0, len(emojis))
	for _, emoji := range emojis {
		if emoji != nil && !emoji.Disabled {
			res = append(res, ToEmoji(emoji))
		}
	}

	return res
}

// absoluteURL turns a site-relative path into an absolute URL.
func absoluteURL(raw string) string {
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return strings.TrimRight(systemsettings.GetBaseURL(), "/") + raw
	}

	return raw
}
