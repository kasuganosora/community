package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
)

type batchEmojiRequest struct {
	IDs      []string  `json:"ids"`
	Action   string    `json:"action"`
	Category *string   `json:"category"`
	License  *string   `json:"license"`
	Aliases  *[]string `json:"aliases"`
	Value    *bool     `json:"value"`
}

func BatchCustomEmojis(c *gin.Context) {
	var req batchEmojiRequest
	if !decodeEmojiJSON(c, &req) {
		return
	}
	if len(req.IDs) == 0 || len(req.IDs) > 100 {
		c.JSON(422, gin.H{"error": "select 1–100 emojis"})
		return
	}
	ids := []uint64{}
	seen := map[uint64]bool{}
	for _, value := range req.IDs {
		id, err := strconv.ParseUint(value, 10, 64)
		if err != nil || id == 0 {
			c.JSON(422, gin.H{"error": "invalid emoji id"})
			return
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	switch req.Action {
	case "delete", "enable", "disable":
	case "set_category":
		if req.Category == nil {
			c.JSON(422, gin.H{"error": "category is required"})
			return
		}
	case "set_license":
		if req.License == nil {
			c.JSON(422, gin.H{"error": "license is required"})
			return
		}
	case "set_aliases", "add_aliases", "remove_aliases":
		if req.Aliases == nil {
			c.JSON(422, gin.H{"error": "aliases are required"})
			return
		}
	case "set_local_only", "set_sensitive":
		if req.Value == nil {
			c.JSON(422, gin.H{"error": "value is required"})
			return
		}
	default:
		c.JSON(422, gin.H{"error": "unsupported batch action"})
		return
	}
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		var emojis []*models.Emoji
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids).Order("id ASC").Find(&emojis).Error; err != nil {
			return err
		}
		if len(emojis) != len(ids) {
			return gorm.ErrRecordNotFound
		}
		for _, e := range emojis {
			if e.Domain != "" && req.Action != "delete" && req.Action != "enable" && req.Action != "disable" {
				return emojiAPIError{422, "remote metadata cannot be modified; copy locally first"}
			}
			switch req.Action {
			case "delete":
				if err := deleteEmoji(tx, e); err != nil {
					return err
				}
				continue
			case "enable":
				e.Disabled = false
			case "disable":
				e.Disabled = true
			case "set_category":
				e.Category = *req.Category
			case "set_license":
				e.License = *req.License
			case "set_aliases":
				e.Aliases = *req.Aliases
			case "add_aliases":
				e.Aliases = append(e.Aliases, (*req.Aliases)...)
			case "remove_aliases":
				remove := map[string]bool{}
				for _, alias := range *req.Aliases {
					remove[alias] = true
				}
				kept := []string{}
				for _, alias := range e.Aliases {
					if !remove[alias] {
						kept = append(kept, alias)
					}
				}
				e.Aliases = kept
			case "set_local_only":
				e.LocalOnly = *req.Value
			case "set_sensitive":
				e.IsSensitive = *req.Value
			}
			if err := normalizeEmoji(e); err != nil {
				return err
			}
			if err := tx.Save(e).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		emojiError(c, err)
		return
	}
	c.JSON(200, gin.H{"updated": len(ids)})
}
