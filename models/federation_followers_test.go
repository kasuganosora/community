package models

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFederationFollowersLargeLocalAudienceAndRemotePages(t *testing.T) {
	conn := feedTestDB(t)
	require.NoError(t, conn.Migrator().DropTable("follows", "accounts"))
	require.NoError(t, conn.Exec("CREATE TABLE accounts (id BIGINT PRIMARY KEY, domain VARCHAR(128), deleted_at TIMESTAMP NULL)").Error)
	require.NoError(t, conn.Exec("CREATE TABLE follows (id BIGINT PRIMARY KEY, account_id BIGINT, follow_id BIGINT, status INT)").Error)
	// More than MySQL's prepared-statement parameter ceiling. None of these
	// local accounts should be loaded by a federation recipient query.
	for start := 1; start <= 66000; start += 1000 {
		values := make([]string, 0, 1000)
		for id := start; id < start+1000; id++ {
			values = append(values, fmt.Sprintf("(%d,NULL,NULL)", id))
		}
		require.NoError(t, conn.Exec("INSERT INTO accounts VALUES "+strings.Join(values, ",")).Error)
	}
	require.NoError(t, conn.Exec("INSERT INTO follows SELECT id,id,1,0 FROM accounts").Error)
	for start := 66001; start <= 67025; start += 100 {
		values := []string{}
		for id := start; id < start+100 && id <= 67025; id++ {
			values = append(values, fmt.Sprintf("(%d,'remote.example',NULL)", id))
		}
		require.NoError(t, conn.Exec("INSERT INTO accounts VALUES "+strings.Join(values, ",")).Error)
	}
	require.NoError(t, conn.Exec("INSERT INTO follows SELECT id,id,1,0 FROM accounts WHERE id>66000").Error)
	require.NoError(t, conn.Exec("UPDATE follows SET status=1 WHERE id=67024").Error)
	require.NoError(t, conn.Exec("UPDATE accounts SET deleted_at=CURRENT_TIMESTAMP WHERE id=67025").Error)
	// A pre-existing explicit recipient survives; a duplicate remote mention
	// and follower is still represented just once.
	recipients := map[uint64]bool{999999: true, 66001: true}
	require.NoError(t, collectFederationFollowers(conn, 1, recipients))
	require.Len(t, recipients, 1024)
	require.True(t, recipients[999999])
	for id := uint64(66001); id <= 67023; id++ {
		require.True(t, recipients[id])
	}
	require.False(t, recipients[1])
	require.False(t, recipients[67024])
	require.False(t, recipients[67025])
}
