package scripts

import "gorm.io/gorm"

// RemoteReblogDomainScript repairs reblogs received through ActivityPub
// Announce, which were stored without statuses.domain. Local and public
// timelines classify by that column, so such reblogs leaked into
// timelines/public?local=true and the public:local stream. Idempotent and
// portable: a correlated subquery works on SQLite, MySQL and PostgreSQL.
type RemoteReblogDomainScript struct{}

func (RemoteReblogDomainScript) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable("statuses") || !db.Migrator().HasTable("accounts") {
		return nil
	}
	return db.Exec(`UPDATE statuses SET domain = (SELECT accounts.domain FROM accounts WHERE accounts.id = statuses.account_id)
WHERE statuses.domain IS NULL AND EXISTS (SELECT 1 FROM accounts WHERE accounts.id = statuses.account_id AND accounts.domain IS NOT NULL AND accounts.domain <> '')`).Error
}

func (RemoteReblogDomainScript) Down(*gorm.DB) error { return nil }
