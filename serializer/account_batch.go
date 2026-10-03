package serializer

import (
	"gorm.io/gorm"
	"strings"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
)

// The cache only lives for one serialization call. It cannot keep disabled or
// newly added emoji metadata alive across requests, or cache authorization.
type accountSerialization struct {
	options  htmlsafe.RenderOptions
	emojis   map[*models.Account][]*Emoji
	rendered map[*models.Account]*Account
}

func newAccountSerialization(accounts []*models.Account) (*accountSerialization, error) {
	return newAccountSerializationWithDB(db.GetDB(), accounts)
}
func newAccountSerializationWithDB(conn *gorm.DB, accounts []*models.Account) (*accountSerialization, error) {
	if conn != nil {
		if err := conn.Statement.Context.Err(); err != nil {
			return nil, err
		}
	}
	ctx := &accountSerialization{options: contentRenderOptions(), emojis: map[*models.Account][]*Emoji{}, rendered: map[*models.Account]*Account{}}
	names := map[string]bool{}
	remoteIDs := map[uint64]bool{}
	seen := map[*models.Account]bool{}
	for _, a := range accounts {
		if a == nil || seen[a] {
			continue
		}
		seen[a] = true
		tags := models.MatchEmojiTags(models.EmojiText(models.AccountEmojiText(a)))
		if a.Domain == nil || *a.Domain == "" {
			for _, tag := range tags {
				names[strings.Trim(tag, ":")] = true
			}
		} else if a.Emojis == nil && a.Id != 0 && len(tags) > 0 {
			remoteIDs[a.Id] = true
		}
	}
	local := map[string]*models.Emoji{}
	remote := map[uint64][]*models.Emoji{}
	if conn != nil && len(names) > 0 {
		keys := make([]string, 0, len(names))
		for name := range names {
			keys = append(keys, name)
		}
		var emojis []*models.Emoji
		if err := conn.Where("domain = ? AND shortcode IN ? AND disabled = ?", "", keys, false).Find(&emojis).Error; err != nil {
			return nil, err
		}
		for _, e := range emojis {
			local[e.Shortcode] = e
		}
	}
	if conn != nil && len(remoteIDs) > 0 {
		ids := make([]uint64, 0, len(remoteIDs))
		for id := range remoteIDs {
			ids = append(ids, id)
		}
		var bindings []struct {
			models.Emoji
			OwnerID uint64
		}
		if err := conn.Model(&models.Emoji{}).Select("emojis.*, account_emojis.account_id AS owner_id").Joins("JOIN account_emojis ON account_emojis.emoji_id = emojis.id").Where("account_emojis.account_id IN ? AND emojis.disabled = ?", ids, false).Scan(&bindings).Error; err != nil {
			return nil, err
		}
		for i := range bindings {
			row := &bindings[i]
			remote[row.OwnerID] = append(remote[row.OwnerID], &row.Emoji)
		}
	}
	for a := range seen {
		domain := ""
		if a.Domain != nil {
			domain = *a.Domain
		}
		source := append([]*models.Emoji{}, a.Emojis...)
		if domain == "" {
			for _, e := range local {
				source = append(source, e)
			}
		} else if a.Emojis == nil {
			source = append(source, remote[a.Id]...)
		}
		ctx.emojis[a] = ToEmojis(models.EmojisInText(source, models.AccountEmojiText(a), domain, false))
	}
	return ctx, nil
}
func (ctx *accountSerialization) account(a *models.Account) *Account {
	if a == nil {
		return nil
	}
	if cached, ok := ctx.rendered[a]; ok {
		return cached
	}
	result := toAccount(a, ctx.options, ctx.emojis[a])
	ctx.rendered[a] = result
	return result
}
