package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEmailDomainCandidates(t *testing.T) {
	assert.Equal(t, []string{"a.b.example.com", "b.example.com", "example.com"}, EmailDomainCandidates("U@A.B.Example.COM"))
	assert.Equal(t, []string{"example.com"}, EmailDomainCandidates("u@example.com"))
	assert.Equal(t, []string{"xn--fiqs8s"}, EmailDomainCandidates("u@xn--fiqs8s"))
	assert.Nil(t, EmailDomainCandidates("nodomain"))
	assert.Equal(t, "xn--bcher-kva.example", NormalizeEmailDomain("@Bücher.example"))
}

func TestEmailDomainBlocked(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	blocked, err := EmailDomainBlocked(db, "a@spam.example")
	require.NoError(t, err)
	assert.False(t, blocked, "missing table blocks nothing")

	require.NoError(t, db.AutoMigrate(&EmailDomainBlock{}))
	require.NoError(t, db.Create(&EmailDomainBlock{Domain: "spam.example"}).Error)
	for email, want := range map[string]bool{
		"a@spam.example": true, "a@SPAM.example": true, "a@x.y.spam.example": true,
		"a@notspam.example": false, "a@example": false, "a@example.com": false,
	} {
		got, err := EmailDomainBlocked(db, email)
		require.NoError(t, err)
		assert.Equal(t, want, got, email)
	}
}
