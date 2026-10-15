package scripts

import (
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// InstanceMetadataScript creates instance_metadata, the persisted nodeinfo
// cache (name, software, version, icon, failure backoff) per remote domain.
type InstanceMetadataScript struct{}

func (InstanceMetadataScript) Up(db *gorm.DB) error {
	return db.AutoMigrate(&models.InstanceMetadata{})
}

func (InstanceMetadataScript) Down(db *gorm.DB) error {
	return db.Migrator().DropTable(&models.InstanceMetadata{})
}
