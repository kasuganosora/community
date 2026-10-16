package admin

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// KeyAvatarDecorationLimit is how many decorations one user may wear at once.
// Misskey's role policy avatarDecorationLimit defaults to 1; 16 is Misskey's
// hard maximum.
const KeyAvatarDecorationLimit = "config.avatar_decorations.limit"

const defaultAvatarDecorationLimit = 1

// AvatarDecorationLimit returns the configured per-user decoration limit.
func AvatarDecorationLimit() int {
	v := systemsettings.GetInt(KeyAvatarDecorationLimit, defaultAvatarDecorationLimit)
	if v < 1 {
		return defaultAvatarDecorationLimit
	}
	if v > models.MaxAvatarDecorations {
		return models.MaxAvatarDecorations
	}
	return v
}

func avatarDecorationSettings() gin.H {
	return gin.H{"limit": AvatarDecorationLimit(), "max_limit": models.MaxAvatarDecorations}
}

// PublicAvatarDecorationSettings tells the picker how many it may select.
func PublicAvatarDecorationSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(200, avatarDecorationSettings())
}

// AdminAvatarDecorationSettings returns the decoration policy.
func AdminAvatarDecorationSettings(c *gin.Context) {
	c.JSON(200, avatarDecorationSettings())
}

// UpdateAvatarDecorationSettings changes the per-user decoration limit.
// Lowering it does not rewrite existing selections; it applies the next time
// a user saves.
func UpdateAvatarDecorationSettings(c *gin.Context) {
	var req struct {
		Limit *int `json:"limit"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Limit == nil {
		c.JSON(400, gin.H{"error": "limit is required"})
		return
	}
	if *req.Limit < 1 || *req.Limit > models.MaxAvatarDecorations {
		c.JSON(422, gin.H{"error": "limit must be between 1 and " + strconv.Itoa(models.MaxAvatarDecorations)})
		return
	}
	if err := systemsettings.SaveChanges(map[string]any{KeyAvatarDecorationLimit: *req.Limit}); err != nil {
		c.JSON(500, gin.H{"error": "operation failed"})
		return
	}
	c.JSON(200, avatarDecorationSettings())
}

type avatarDecorationInput struct {
	ID      string  `json:"id"`
	Angle   float64 `json:"angle"`
	FlipH   bool    `json:"flip_h"`
	OffsetX float64 `json:"offset_x"`
	OffsetY float64 `json:"offset_y"`
}

var errDecorationLimit = errors.New("too many decorations")

// SelectAvatarDecorations replaces the current user's decoration list.
// PUT /api/v1/accounts/avatar_decorations {"decorations":[{id,angle,flip_h,offset_x,offset_y}]}
func SelectAvatarDecorations(c *gin.Context) {
	account := controllers.GetAccount(c)
	if account == nil {
		c.Status(401)
		return
	}
	var req struct {
		Decorations []avatarDecorationInput `json:"decorations"`
	}
	if c.ShouldBindJSON(&req) != nil {
		c.JSON(400, gin.H{"error": "invalid JSON"})
		return
	}
	limit := AvatarDecorationLimit()
	if len(req.Decorations) > limit {
		c.JSON(422, gin.H{"error": "at most " + strconv.Itoa(limit) + " decorations may be selected", "limit": limit})
		return
	}
	list := make(models.AvatarDecorationSelections, 0, len(req.Decorations))
	ids := make([]uint64, 0, len(req.Decorations))
	for _, in := range req.Decorations {
		id, err := strconv.ParseUint(strings.TrimSpace(in.ID), 10, 64)
		if err != nil || id == 0 {
			c.JSON(422, gin.H{"error": "invalid decoration id"})
			return
		}
		ids = append(ids, id)
		list = append(list, models.AvatarDecorationSelection{ID: strconv.FormatUint(id, 10), Angle: in.Angle, FlipH: in.FlipH, OffsetX: in.OffsetX, OffsetY: in.OffsetY}.Normalized())
	}
	var first uint64
	if len(ids) > 0 {
		first = ids[0]
	}
	err := db.GetDB().Transaction(func(tx *gorm.DB) error {
		if len(ids) > 0 {
			unique := map[uint64]bool{}
			for _, id := range ids {
				unique[id] = true
			}
			want := make([]uint64, 0, len(unique))
			for id := range unique {
				want = append(want, id)
			}
			var rows []models.AvatarDecoration
			if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ? AND enabled = ?", want, true).Find(&rows).Error; e != nil {
				return e
			}
			if len(rows) != len(want) {
				return gorm.ErrRecordNotFound
			}
		}
		var value models.AvatarDecorationSelections
		if len(list) > 0 {
			value = list
		}
		return tx.Model(&models.Account{}).Where("id = ?", account.Id).UpdateColumns(map[string]any{"avatar_decoration_id": first, "avatar_decorations": value}).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(422, gin.H{"error": "decoration is not available"})
		return
	}
	if err != nil {
		assetError(c, err)
		return
	}
	account.AvatarDecorationID = first
	account.AvatarDecorations = nil
	if len(list) > 0 {
		account.AvatarDecorations = list
	}
	_ = cache.InvalidateAccount(c.Request.Context(), account.Id)
	out := serializer.AvatarDecorations(account)
	if out == nil {
		out = []serializer.AvatarDecoration{}
	}
	c.JSON(200, gin.H{"avatar_decorations": out, "limit": limit})
}

// removeDecorationFromAccounts drops a deleted decoration from every local
// account's list and returns the accounts that changed.
func removeDecorationFromAccounts(tx *gorm.DB, id uint64) ([]uint64, error) {
	needle := `%"id":"` + strconv.FormatUint(id, 10) + `"%`
	var rows []models.Account
	if err := tx.Select("id", "avatar_decorations", "avatar_decoration_id", "domain").
		Where("domain IS NULL AND avatar_decorations LIKE ?", needle).Find(&rows).Error; err != nil {
		return nil, err
	}
	changed := make([]uint64, 0, len(rows))
	for _, row := range rows {
		kept := make(models.AvatarDecorationSelections, 0, len(row.AvatarDecorations))
		for _, d := range row.AvatarDecorations {
			if d.LocalID() != id {
				kept = append(kept, d)
			}
		}
		var first uint64
		for _, d := range kept {
			if first = d.LocalID(); first != 0 {
				break
			}
		}
		var value models.AvatarDecorationSelections
		if len(kept) > 0 {
			value = kept
		}
		if err := tx.Model(&models.Account{}).Where("id = ?", row.Id).UpdateColumns(map[string]any{"avatar_decorations": value, "avatar_decoration_id": first}).Error; err != nil {
			return nil, err
		}
		changed = append(changed, row.Id)
	}
	return changed, nil
}
