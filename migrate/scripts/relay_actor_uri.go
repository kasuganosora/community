package scripts

import (
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// RelayActorURIScript adds relays.actor_uri: the local actor subscribed to a
// relay. Relays migrated from Misskey are followed by relay.actor
// (<base>/users/<id>), and the relay only recognises that actor. Empty means
// the instance actor. Idempotent.
type RelayActorURIScript struct{}

func (RelayActorURIScript) Up(db *gorm.DB) error {
	if database.IsXmlMode() || !db.Migrator().HasTable(&models.Relay{}) {
		return nil
	}
	if db.Migrator().HasColumn(&models.Relay{}, "ActorURI") {
		return nil
	}
	return db.Migrator().AddColumn(&models.Relay{}, "ActorURI")
}

func (RelayActorURIScript) Down(db *gorm.DB) error {
	if database.IsXmlMode() || !db.Migrator().HasColumn(&models.Relay{}, "ActorURI") {
		return nil
	}
	return db.Migrator().DropColumn(&models.Relay{}, "ActorURI")
}
