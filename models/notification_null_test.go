package models

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Optional notification references are foreign keys; zero values must not be
// inserted (MySQL would reject report_id=0 with FK error 1452).
func TestNotification_ZeroRefsOmittedOnInsert(t *testing.T) {
	gdb, err := gorm.Open(mysql.New(mysql.Config{
		DSN:                       "u:p@tcp(127.0.0.1:1)/x",
		SkipInitializeWithVersion: true,
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	require.NoError(t, err)

	stmt := gdb.Session(&gorm.Session{DryRun: true}).
		Create(&Notification{Type: 1, AccountId: 1, FromAccountId: 2, StatusId: 3}).Statement
	sql := stmt.SQL.String()

	assert.Contains(t, sql, "`status_id`")
	assert.Contains(t, sql, "`from_account_id`")
	assert.False(t, strings.Contains(sql, "`report_id`"), sql)
	assert.False(t, strings.Contains(sql, "`list_id`"), sql)
}
