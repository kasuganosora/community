package scripts

import (
	"github.com/kasuganosora/community/activitypub"
	"gorm.io/gorm"
)

// AddDeliveryTargetCooldownScript creates activitypub_delivery_targets, the
// per-inbox cooldown for unreachable federation targets. Idempotent.
type AddDeliveryTargetCooldownScript struct{}

func (AddDeliveryTargetCooldownScript) Up(tx *gorm.DB) error {
	return tx.AutoMigrate(&activitypub.DeliveryTarget{})
}

func (AddDeliveryTargetCooldownScript) Down(tx *gorm.DB) error {
	return tx.Migrator().DropTable(&activitypub.DeliveryTarget{})
}
