package serializer

import (
	"errors"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type Reaction struct {
	Name        string     `json:"name"`
	Count       int64      `json:"count"`
	Me          bool       `json:"me"`
	URL         string     `json:"url,omitempty"`
	StaticURL   string     `json:"static_url,omitempty"`
	IsSensitive bool       `json:"is_sensitive"`
	Accounts    []*Account `json:"accounts,omitempty"`
}

func StatusReactions(statusID uint64, viewer *models.Account, accounts bool) ([]*Reaction, error) {
	batch, err := StatusReactionsBatch([]uint64{statusID}, viewer)
	if err != nil {
		return nil, err
	}
	result := batch[statusID]
	if accounts {
		conn := db.GetDB()
		for _, r := range result {
			var actors []*models.Account
			if err := conn.Joins("JOIN status_reactions ON status_reactions.account_id = accounts.id").Where("status_reactions.status_id = ? AND "+models.ReactionNameSQL(conn)+" = ?", statusID, r.Name).Order("status_reactions.id DESC").Limit(20).Find(&actors).Error; err != nil {
				return nil, err
			}
			r.Accounts = make([]*Account, 0)
			for _, a := range actors {
				blocked, err := models.AccountsBlocked(conn, viewer, a)
				if err != nil {
					return nil, err
				}
				if !blocked && !a.IsBan {
					r.Accounts = append(r.Accounts, ToAccount(a))
				}
			}
		}
	}
	return result, nil
}

// A timeline resolves grouped counts, viewer reactions and emoji metadata with
// three queries for the whole page, including statuses wrapped in reblogs.
func StatusReactionsBatch(statusIDs []uint64, viewer *models.Account) (map[uint64][]*Reaction, error) {
	return statusReactionsBatchWithDB(db.GetDB(), statusIDs, viewer)
}
func statusReactionsBatchWithDB(conn *gorm.DB, statusIDs []uint64, viewer *models.Account) (map[uint64][]*Reaction, error) {
	if conn != nil {
		if err := conn.Statement.Context.Err(); err != nil {
			return nil, err
		}
	}
	result := make(map[uint64][]*Reaction, len(statusIDs))
	for _, id := range statusIDs {
		result[id] = make([]*Reaction, 0)
	}
	if len(statusIDs) == 0 {
		return result, nil
	}
	if conn == nil {
		if models.ReactionSchemaRequired() {
			return nil, errors.New("reaction database is unavailable")
		}
		return result, nil
	}
	// Server startup requires the migrated table. Only explicitly partial unit
	// test/migration schemas take the compatibility probe; production never does.
	if !models.ReactionSchemaRequired() && !conn.Migrator().HasTable(&models.StatusReaction{}) {
		return result, nil
	}
	type group struct {
		StatusID uint64
		Name     string
		EmojiID  uint64
		Count    int64
	}
	var groups []group
	nameSQL := models.ReactionNameSQL(conn)
	if err := conn.Model(&models.StatusReaction{}).Select("status_id, "+nameSQL+" AS name, emoji_id, COUNT(*) AS count").Where("status_id IN ?", statusIDs).Group("status_id, " + nameSQL + ", emoji_id").Order("MIN(id)").Scan(&groups).Error; err != nil {
		return nil, err
	}
	emojiIDs := make([]uint64, 0)
	for _, g := range groups {
		if g.EmojiID != 0 {
			emojiIDs = append(emojiIDs, g.EmojiID)
		}
	}
	emojis := map[uint64]*models.Emoji{}
	if len(emojiIDs) > 0 {
		var rows []*models.Emoji
		if err := conn.Where("id IN ? AND disabled = ?", emojiIDs, false).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, e := range rows {
			emojis[e.Id] = e
		}
	}
	mine := map[uint64]string{}
	if viewer != nil {
		var rows []models.StatusReaction
		if err := conn.Where("status_id IN ? AND account_id = ?", statusIDs, viewer.Id).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, r := range rows {
			mine[r.StatusID] = r.Name
		}
	}
	for _, g := range groups {
		if len(result[g.StatusID]) >= 100 {
			continue
		}
		r := &Reaction{Name: g.Name, Count: g.Count, Me: mine[g.StatusID] == g.Name}
		if e := emojis[g.EmojiID]; e != nil {
			r.URL = mediaproxy.URL(mediaproxy.KindEmoji, e.Url)
			r.StaticURL = mediaproxy.URL(mediaproxy.KindEmoji, e.StaticUrl)
			r.IsSensitive = e.IsSensitive
		}
		result[g.StatusID] = append(result[g.StatusID], r)
	}
	return result, nil
}
