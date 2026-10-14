package scripts

import "gorm.io/gorm"

// DropDiscoveryAttemptedIndexScript removes the index on
// accounts.remote_discovery_attempted_at. The backfill selection never uses it
// selectively (OR IS NULL + CASE ordering), while it forced every
// attempted_at write to update an index and made HOT updates impossible.
// Idempotent; Down recreates it.
type DropDiscoveryAttemptedIndexScript struct{}

const discoveryAttemptedIndex = "idx_accounts_remote_discovery_attempted_at"

func (DropDiscoveryAttemptedIndexScript) Up(conn *gorm.DB) error {
	if !conn.Migrator().HasIndex("accounts", discoveryAttemptedIndex) {
		return nil
	}
	return conn.Migrator().DropIndex("accounts", discoveryAttemptedIndex)
}

func (DropDiscoveryAttemptedIndexScript) Down(conn *gorm.DB) error {
	if conn.Migrator().HasIndex("accounts", discoveryAttemptedIndex) {
		return nil
	}
	return conn.Exec("CREATE INDEX " + discoveryAttemptedIndex + " ON accounts(remote_discovery_attempted_at)").Error
}
