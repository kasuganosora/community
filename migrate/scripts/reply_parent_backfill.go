package scripts

import (
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// ReplyParentBackfillScript links replies to parents this server already
// has, using a stored inReplyTo address or one local status URL in the body.
// It does not fetch remote objects. Safe to run more than once.
type ReplyParentBackfillScript struct{}

func (ReplyParentBackfillScript) Up(db *gorm.DB) error {
	_, err := models.RepairUnlinkedReplyParents(db, systemsettings.GetBaseURL())
	return err
}

func (ReplyParentBackfillScript) Down(*gorm.DB) error { return nil }
