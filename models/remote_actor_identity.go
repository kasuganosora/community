package models

// A fixed-size hash key serializes first import without indexing/truncating
// arbitrary-length actor URLs. URI is compared after locking to fail closed
// even if a digest collision were ever presented.
type RemoteActorIdentity struct {
	URIHash   string   `gorm:"primaryKey;type:char(64)"`
	URI       string   `gorm:"type:text;not null"`
	AccountID *uint64  `gorm:"uniqueIndex"`
	Account   *Account `gorm:"foreignKey:AccountID"`
}
