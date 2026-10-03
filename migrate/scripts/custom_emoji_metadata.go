package scripts

import (
	"fmt"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type CustomEmojiMetadataScript struct{}

func (CustomEmojiMetadataScript) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&models.Emoji{}, &models.EmojiUpload{}); err != nil {
		return err
	}
	for _, name := range []string{"uni_emojis_shortcode", "shortcode"} {
		if db.Migrator().HasConstraint(&models.Emoji{}, name) {
			if err := db.Migrator().DropConstraint(&models.Emoji{}, name); err != nil {
				return err
			}
		} else if db.Migrator().HasIndex(&models.Emoji{}, name) {
			if err := db.Migrator().DropIndex(&models.Emoji{}, name); err != nil {
				return err
			}
		}
	}
	return nil
}
func (CustomEmojiMetadataScript) Down(*gorm.DB) error {
	return fmt.Errorf("emoji metadata migration is irreversible: multiple domains may share a shortcode")
}
