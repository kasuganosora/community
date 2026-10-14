package models

import (
	"testing"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFeedAsyncCommitIsTransactionLocal(t *testing.T) {
	if !pgtest.Enabled() {
		t.Skip("PostgreSQL only")
	}
	conn := feedTestDB(t)
	require.True(t, dialect.IsPostgres(conn))
	var inside, after string
	require.NoError(t, conn.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, feedAsyncCommit(tx))
		return tx.Raw("SHOW synchronous_commit").Scan(&inside).Error
	}))
	require.NoError(t, conn.Raw("SHOW synchronous_commit").Scan(&after).Error)
	require.Equal(t, "off", inside)
	require.Equal(t, "on", after)
}
