package scripts

import (
	"github.com/kasuganosora/community/datatransfer"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// DataTransferScript preserves existing identities while adding import
// provenance and resumable jobs. Down does not drop migrated user data.
type DataTransferScript struct{}

func (DataTransferScript) Up(db *gorm.DB) error {
	// Only touch the columns owned by this feature. Re-running AutoMigrate on
	// the complete account graph would also try to rewrite legacy join-table
	// foreign keys (some old account_id columns are varchar), unrelated to an
	// identity-preserving import and incompatible with existing installations.
	for _, change := range []struct {
		model  any
		fields []string
	}{
		{&models.Account{}, []string{"LocalActorURI", "ImportedNoteHTML"}},
		{&models.Status{}, []string{"LocalObjectURI", "LocalOnly"}},
		{&models.AccountKey{}, []string{"KeyID"}},
		{&models.UserSecretData{}, []string{"PasswordScheme"}},
	} {
		for _, field := range change.fields {
			if !db.Migrator().HasColumn(change.model, field) {
				if err := db.Migrator().AddColumn(change.model, field); err != nil {
					return err
				}
			}
		}
	}
	if err := db.Migrator().AlterColumn(&models.User{}, "UserName"); err != nil {
		return err
	}
	if err := db.Migrator().AlterColumn(&models.Account{}, "Note"); err != nil {
		return err
	}
	if err := db.Migrator().AlterColumn(&models.Poll{}, "ExpiresAt"); err != nil {
		return err
	}
	return db.AutoMigrate(&models.AccountFederationKey{}, &datatransfer.Job{},
		&datatransfer.ImportReceipt{}, &datatransfer.StoredPreservedRecord{}, &datatransfer.ImportedMediaAlias{})
}

func (DataTransferScript) Down(*gorm.DB) error { return nil }

// BlurHash can encode up to 9 x 9 components (166 characters). The old
// 50-character column rejected valid Misskey attachments on strict MySQL.
// Keep this repair independently registered so existing transfer deployments
// also widen the column without rerunning their completed identity migration.
type MediaBlurhashCapacityScript struct{}

func (MediaBlurhashCapacityScript) Up(db *gorm.DB) error {
	return db.Migrator().AlterColumn(&models.MediaAttachment{}, "Blurhash")
}

func (MediaBlurhashCapacityScript) Down(*gorm.DB) error { return nil }
