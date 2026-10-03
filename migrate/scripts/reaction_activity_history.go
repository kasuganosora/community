package scripts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ReactionActivityHistoryScript struct{}

func (ReactionActivityHistoryScript) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&models.RemoteReactionActivity{}); err != nil {
		return err
	}
	var cursor uint64
	for {
		var rows []models.StatusReaction
		if err := db.Joins("JOIN accounts ON accounts.id = status_reactions.account_id").Where("accounts.domain IS NOT NULL AND status_reactions.id > ? AND status_reactions.activity_uri <> ?", cursor, "").Order("status_reactions.id ASC").Limit(500).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		records := make([]models.RemoteReactionActivity, 0, len(rows))
		for _, row := range rows {
			digest := sha256.Sum256([]byte(row.ActivityURI))
			records = append(records, models.RemoteReactionActivity{AccountID: row.AccountID, ActivityHash: hex.EncodeToString(digest[:]), ActivityURI: row.ActivityURI, StatusID: row.StatusID, Name: row.Name})
			cursor = row.ID
		}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(records, 100).Error; err != nil {
			return err
		}
	}
}
func (ReactionActivityHistoryScript) Down(*gorm.DB) error {
	return fmt.Errorf("reaction history rollback would discard replay and revocation protection")
}
