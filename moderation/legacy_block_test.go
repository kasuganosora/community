package moderation

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLiftLegacyDomainBlock(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open("file::memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, LiftLegacyDomainBlock(conn, "x.example", "suspend"), "missing table is fine")
	require.NoError(t, conn.AutoMigrate(&models.DomainBlock{}))
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: "Bad.Example", Severity: "suspend"}).Error)
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: "quiet.example", Severity: "silence"}).Error)
	require.NoError(t, conn.Create(&models.DomainBlock{Domain: "sub.bad.example", Severity: "suspend"}).Error)

	suspend, silence, err := models.DomainSeverities(conn)
	require.NoError(t, err)
	require.True(t, suspend.Contains("bad.example"))

	require.NoError(t, LiftLegacyDomainBlock(conn, "quiet.example", "suspend"), "wrong severity leaves the row")
	_, silence, _ = models.DomainSeverities(conn)
	require.True(t, silence.Contains("quiet.example"))

	require.NoError(t, LiftLegacyDomainBlock(conn, "bad.example", "suspend"))
	require.NoError(t, LiftLegacyDomainBlock(conn, "bad.example", "suspend"), "idempotent")
	require.NoError(t, LiftLegacyDomainBlock(conn, "quiet.example", "silence"))
	suspend, silence, err = models.DomainSeverities(conn)
	require.NoError(t, err)
	require.False(t, suspend["bad.example"])
	require.True(t, suspend["sub.bad.example"], "only the exact domain is lifted")
	require.False(t, silence.Contains("quiet.example"))
}
