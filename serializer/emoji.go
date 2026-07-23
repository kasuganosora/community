package serializer

import "github.com/kasuganosora/community/models"

type Emoji struct {
	Shortcode       string `json:"shortcode"`
	Url             string `json:"url"`
	StaticUrl       string `json:"static_url"`
	VisibleInPicker bool   `json:"visible_in_picker"`
	Category        string `json:"category"`
}

func ToEmoji(emoji *models.Emoji) *Emoji {
	return &Emoji{
		Shortcode:       emoji.Shortcode,
		Url:             emoji.Url,
		StaticUrl:       emoji.StaticUrl,
		VisibleInPicker: emoji.VisibleInPicker,
		Category:        emoji.Category,
	}
}

func ToEmojis(emojis []*models.Emoji) []*Emoji {
	var res = make([]*Emoji, 0, len(emojis))
	for _, emoji := range emojis {
		res = append(res, ToEmoji(emoji))
	}

	return res
}
