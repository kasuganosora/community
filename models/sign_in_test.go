package models

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRecordSignInRotatesPreviousLoginAndIgnoresNonIPs(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&User{}))
	user := User{UserName: "ler", EmailHash: "ler-email"}
	require.NoError(t, conn.Create(&user).Error)

	first := time.Date(2026, 10, 1, 13, 0, 45, 0, time.UTC)
	require.NoError(t, RecordSignIn(conn, user.Id, " 203.0.113.10 ", first))
	require.NoError(t, conn.First(&user, user.Id).Error)
	require.NotNil(t, user.CurrentSignInAt)
	require.True(t, user.CurrentSignInAt.Equal(first))
	require.Nil(t, user.LastSignInAt)
	require.Equal(t, "203.0.113.10", user.CurrentSignInIP)
	require.Empty(t, user.LastSignInIP)
	require.Equal(t, 1, user.SignInCount)

	second := first.Add(time.Hour)
	require.NoError(t, RecordSignIn(conn, user.Id, "not-an-ip", second))
	require.NoError(t, conn.First(&user, user.Id).Error)
	require.True(t, user.CurrentSignInAt.Equal(second))
	require.NotNil(t, user.LastSignInAt)
	require.True(t, user.LastSignInAt.Equal(first))
	require.Empty(t, user.CurrentSignInIP)
	require.Equal(t, "203.0.113.10", user.LastSignInIP)
	require.Equal(t, 2, user.SignInCount)
}

func TestNormalizeSignInIP(t *testing.T) {
	require.Equal(t, "2001:db8::1", NormalizeSignInIP("2001:db8::1"))
	require.Equal(t, "203.0.113.10", NormalizeSignInIP("::ffff:203.0.113.10"))
	require.Empty(t, NormalizeSignInIP(""))
	require.Empty(t, NormalizeSignInIP("example.test"))
}
