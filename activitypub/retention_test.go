package activitypub

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

var retentionUserContentTables = []string{"statuses", "notifications", "favourites", "bookmarks", "media_attachments"}

func retentionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, conn.AutoMigrate(&ProcessedActivity{}, &Delivery{}))
	// Stand-ins for local user content: the sweep must never touch them,
	// however old their rows are.
	for _, table := range retentionUserContentTables {
		require.NoError(t, conn.Exec("CREATE TABLE "+table+" (id integer primary key, status varchar(20), created_at datetime, updated_at datetime)").Error)
	}
	return conn
}

func TestRetentionSweepDeletesOnlyExpiredBookkeeping(t *testing.T) {
	conn := retentionTestDB(t)
	now := time.Now()
	old := now.Add(-200 * 24 * time.Hour)
	recent := now.Add(-time.Hour)

	for i := range 23 {
		ts := old
		if i%5 == 0 {
			ts = recent
		}
		require.NoError(t, conn.Create(&ProcessedActivity{ActivityID: fmt.Sprintf("https://r.example/a/%d", i), CreatedAt: &ts}).Error)
	}
	for i, st := range []DeliveryStatus{DeliveryStatusDelivered, DeliveryStatusDelivered, DeliveryStatusPending, DeliveryStatusProcessing, DeliveryStatusFailed} {
		require.NoError(t, conn.Create(&Delivery{ActivityID: fmt.Sprintf("x%d", i), TargetInbox: "https://r.example/inbox", Payload: "{}", Status: st}).Error)
	}
	// Everything is old except the second delivered row.
	require.NoError(t, conn.Exec("UPDATE activitypub_deliveries SET updated_at = ?, created_at = ?", old, old).Error)
	require.NoError(t, conn.Exec("UPDATE activitypub_deliveries SET updated_at = ? WHERE activity_id = 'x1'", recent).Error)
	for _, table := range retentionUserContentTables {
		for i := 1; i <= 3; i++ {
			require.NoError(t, conn.Exec("INSERT INTO "+table+" (id, status, created_at, updated_at) VALUES (?, 'delivered', ?, ?)", i, old, old).Error)
		}
	}

	rules := []retentionRule{
		{table: "activitypub_processed_activities", col: "created_at", keep: 90 * 24 * time.Hour},
		{table: "activitypub_deliveries", where: "status = ?", args: []any{"delivered"}, col: "updated_at", keep: 30 * 24 * time.Hour},
		{table: "missing_table", col: "created_at", keep: time.Hour},
	}
	removed := sweepRetention(context.Background(), conn, rules, now, 4, 0)
	require.Equal(t, int64(18), removed["activitypub_processed_activities"], "small batches loop until done")
	require.Equal(t, int64(1), removed["activitypub_deliveries"])

	var left []string
	require.NoError(t, conn.Model(&ProcessedActivity{}).Order("id").Pluck("activity_id", &left).Error)
	require.Len(t, left, 5)
	var statuses []string
	require.NoError(t, conn.Model(&Delivery{}).Order("id").Pluck("status", &statuses).Error)
	require.Equal(t, []string{"delivered", "pending", "processing", "failed"}, statuses, "pending/processing/failed are never deleted")

	for _, table := range retentionUserContentTables {
		var n int64
		require.NoError(t, conn.Table(table).Count(&n).Error)
		require.Equal(t, int64(3), n, "user content in %s untouched", table)
	}

	// Idempotent.
	require.Empty(t, sweepRetention(context.Background(), conn, rules, now, 4, 0))
}

func TestRetentionDefaultRulesNeverTouchUserContent(t *testing.T) {
	conn := retentionTestDB(t)
	old := time.Now().Add(-10 * 365 * 24 * time.Hour)
	for _, table := range retentionUserContentTables {
		require.NoError(t, conn.Exec("INSERT INTO "+table+" (id, status, created_at, updated_at) VALUES (1, 'delivered', ?, ?)", old, old).Error)
	}
	rules := retentionRules()
	require.NotEmpty(t, rules)
	for _, r := range rules {
		require.Contains(t, []string{"activitypub_processed_activities", "activitypub_deliveries"}, r.table)
		if r.table == "activitypub_deliveries" {
			require.Equal(t, []any{"delivered"}, r.args)
		}
	}
	sweepRetention(context.Background(), conn, rules, time.Now(), 10, 0)
	for _, table := range retentionUserContentTables {
		var n int64
		require.NoError(t, conn.Table(table).Count(&n).Error)
		require.Equal(t, int64(1), n, table)
	}
	require.Equal(t, 90, retentionDays("", 90))
	require.Equal(t, 0, retentionDays("0", 90))
	require.Equal(t, 90, retentionDays("-1", 90))
	require.Equal(t, 7, retentionDays(" 7 ", 90))
}

func TestRetentionDedupRecordExpiryIsReprocessable(t *testing.T) {
	// Documented trade-off: once a dedup record expires, the same activity
	// id is no longer considered processed.
	conn := retentionTestDB(t)
	old := time.Now().Add(-100 * 24 * time.Hour)
	require.NoError(t, conn.Create(&ProcessedActivity{ActivityID: "https://r.example/a/old", CreatedAt: &old}).Error)
	sweepRetention(context.Background(), conn, []retentionRule{{table: "activitypub_processed_activities", col: "created_at", keep: 90 * 24 * time.Hour}}, time.Now(), 10, 0)
	var n int64
	require.NoError(t, conn.Model(&ProcessedActivity{}).Where("activity_id = ?", "https://r.example/a/old").Count(&n).Error)
	require.Zero(t, n)
}

func TestRetentionSweepPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	conn, err := gorm.Open(dialect.OpenPostgres(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, conn.Exec(`DROP SCHEMA IF EXISTS ret_sweep_test CASCADE`).Error)
	require.NoError(t, conn.Exec(`CREATE SCHEMA ret_sweep_test`).Error)
	require.NoError(t, conn.Exec(`SET search_path TO ret_sweep_test`).Error)
	t.Cleanup(func() { conn.Exec(`DROP SCHEMA ret_sweep_test CASCADE`) })
	require.NoError(t, conn.AutoMigrate(&ProcessedActivity{}, &Delivery{}))
	old := time.Now().Add(-200 * 24 * time.Hour)
	for i := range 7 {
		require.NoError(t, conn.Create(&ProcessedActivity{ActivityID: fmt.Sprintf("a%d", i), CreatedAt: &old}).Error)
	}
	require.NoError(t, conn.Create(&Delivery{ActivityID: "d", TargetInbox: "i", Payload: "{}", Status: DeliveryStatusDelivered}).Error)
	require.NoError(t, conn.Create(&Delivery{ActivityID: "p", TargetInbox: "i", Payload: "{}", Status: DeliveryStatusPending}).Error)
	require.NoError(t, conn.Exec("UPDATE activitypub_deliveries SET updated_at = ?", old).Error)
	removed := sweepRetention(context.Background(), conn, retentionRules(), time.Now(), 3, 0)
	require.Equal(t, int64(7), removed["activitypub_processed_activities"])
	require.Equal(t, int64(1), removed["activitypub_deliveries"])
}
