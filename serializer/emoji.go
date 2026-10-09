package serializer

import (
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
)

type Emoji struct {
	Shortcode       string   `json:"shortcode"`
	Url             string   `json:"url"`
	StaticUrl       string   `json:"static_url"`
	VisibleInPicker bool     `json:"visible_in_picker"`
	Category        string   `json:"category"`
	Aliases         []string `json:"aliases"`
	License         string   `json:"license"`
	LocalOnly       bool     `json:"local_only"`
	IsSensitive     bool     `json:"is_sensitive"`
}

func ToEmoji(emoji *models.Emoji) *Emoji {
	if emoji == nil {
		return nil
	}
	return &Emoji{
		Shortcode:       emoji.Shortcode,
		Url:             mediaproxy.URL(mediaproxy.KindEmoji, emoji.Url),
		StaticUrl:       mediaproxy.URL(mediaproxy.KindEmoji, emoji.StaticUrl),
		VisibleInPicker: emoji.VisibleInPicker,
		Category:        emoji.Category,
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
