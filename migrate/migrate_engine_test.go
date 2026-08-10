package migrate

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/kasuganosora/community/migrate/scripts"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// Test helpers: controllable fake migration scripts.
//
// The migrate engine discovers scripts from the exported package-level slice
// `scripts.ScripsType` and identifies them by their reflect type name. Tests
// therefore:
//   1. swap in their own set of fake scripts,
//   2. run the engine against an in-memory sqlexec DB, and
//   3. restore the original registry on cleanup.
//
// Each fake is a distinct named struct so GetScripts()/GetScriptsName() produce
// unique names. Scripts that mutate DB state use GORM Migrator (the same path
// the real scripts use) so they exercise the same code paths and are idempotent
// on the sqlexec memory backend.
// ---------------------------------------------------------------------------

// withScripts temporarily replaces the global script registry for the duration
// of a test and restores it afterwards. It must be the first call in a test
// before invoking Up/Down.
func withScripts(t *testing.T, ss ...scripts.MigrationScript) {
	t.Helper()
	original := scripts.ScripsType
	scripts.ScripsType = ss
	t.Cleanup(func() { scripts.ScripsType = original })
}

// fakeWidget / fakeGadget: real, idempotent scripts backed by GORM Migrator.
type fakeWidget struct{}

func (fakeWidget) Up(db *gorm.DB) error   { return db.Migrator().AutoMigrate(&widgetModel{}) }
func (fakeWidget) Down(db *gorm.DB) error { return db.Migrator().DropTable(&widgetModel{}) }

type widgetModel struct {
	ID  int64  `gorm:"primaryKey"`
	Val string `gorm:"type:varchar(32)"`
}

func (widgetModel) TableName() string { return "fake_widgets" }

type fakeGadget struct{}

func (fakeGadget) Up(db *gorm.DB) error   { return db.Migrator().AutoMigrate(&gadgetModel{}) }
func (fakeGadget) Down(db *gorm.DB) error { return db.Migrator().DropTable(&gadgetModel{}) }

type gadgetModel struct {
	ID  int64 `gorm:"primaryKey"`
	Num int
}

func (gadgetModel) TableName() string { return "fake_gadgets" }

// countMigrations returns how many migration records are stored for assertions.
func countMigrations(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&Migration{}).Count(&n).Error)
	return n
}

// migrationNames returns the ordered list of recorded migration names.
func migrationNames(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var ms []*Migration
	require.NoError(t, db.Order("id ASC").Find(&ms).Error)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

// ---------------------------------------------------------------------------
// checkMigrationsTable
// ---------------------------------------------------------------------------

func TestCheckMigrationsTable_CreatesWhenAbsent(t *testing.T) {
	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	require.False(t, db.Migrator().HasTable(&Migration{}), "table must not pre-exist")
	require.NoError(t, checkMigrationsTable(db))
	assert.True(t, db.Migrator().HasTable(&Migration{}))
}

func TestCheckMigrationsTable_IdempotentWhenPresent(t *testing.T) {
	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	// Create once, then call again — must not error and table must remain.
	require.NoError(t, checkMigrationsTable(db))
	require.True(t, db.Migrator().HasTable(&Migration{}))
	require.NoError(t, checkMigrationsTable(db))
	assert.True(t, db.Migrator().HasTable(&Migration{}))
}

// ---------------------------------------------------------------------------
// getLastBatchNum
// ---------------------------------------------------------------------------

func TestGetLastBatchNum_Empty(t *testing.T) {
	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB
	require.NoError(t, checkMigrationsTable(db))

	batch, err := getLastBatchNum(db)
	require.NoError(t, err)
	assert.Equal(t, 0, batch, "empty table must report batch 0 (ErrRecordNotFound is swallowed)")
}

func TestGetLastBatchNum_ReturnsMaxBatch(t *testing.T) {
	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB
	require.NoError(t, checkMigrationsTable(db))

	require.NoError(t, db.Create(&Migration{Name: "a", Batch: 2}).Error)
	require.NoError(t, db.Create(&Migration{Name: "b", Batch: 5}).Error)
	require.NoError(t, db.Create(&Migration{Name: "c", Batch: 3}).Error)

	batch, err := getLastBatchNum(db)
	require.NoError(t, err)
	assert.Equal(t, 5, batch, "must return the maximum batch number regardless of insert order")
}

// ---------------------------------------------------------------------------
// getUnExecutedMigrationsScript
// ---------------------------------------------------------------------------

func TestGetUnExecutedMigrationsScript_AllUnexecuted(t *testing.T) {
	withScripts(t, fakeWidget{}, fakeGadget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB
	require.NoError(t, checkMigrationsTable(db))

	names, err := getUnExecutedMigrationsScript(db)
	require.NoError(t, err)
	assert.Equal(t, []string{"fakeWidget", "fakeGadget"}, names,
		"unexecuted names must follow registration order")
}

func TestGetUnExecutedMigrationsScript_RespectsOrder(t *testing.T) {
	// Registration order is the execution contract; swap the order and verify.
	withScripts(t, fakeGadget{}, fakeWidget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB
	require.NoError(t, checkMigrationsTable(db))

	names, err := getUnExecutedMigrationsScript(db)
	require.NoError(t, err)
	assert.Equal(t, []string{"fakeGadget", "fakeWidget"}, names)
}

func TestGetUnExecutedMigrationsScript_NoneAfterAllRun(t *testing.T) {
	withScripts(t, fakeWidget{}, fakeGadget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB
	require.NoError(t, checkMigrationsTable(db))

	// Record both as already executed.
	require.NoError(t, db.Create(&Migration{Name: "fakeWidget", Batch: 1}).Error)
	require.NoError(t, db.Create(&Migration{Name: "fakeGadget", Batch: 1}).Error)

	names, err := getUnExecutedMigrationsScript(db)
	require.NoError(t, err)
	assert.Empty(t, names, "fully-migrated DB must report nothing pending")
}

func TestGetUnExecutedMigrationsScript_PartialRunKeepsOrder(t *testing.T) {
	withScripts(t, fakeWidget{}, fakeGadget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB
	require.NoError(t, checkMigrationsTable(db))

	// Only the first one ran.
	require.NoError(t, db.Create(&Migration{Name: "fakeWidget", Batch: 1}).Error)

	names, err := getUnExecutedMigrationsScript(db)
	require.NoError(t, err)
	assert.Equal(t, []string{"fakeGadget"}, names,
		"already-executed entries must be filtered out while preserving order")
}

func TestGetUnExecutedMigrationsScript_StaleRecordIgnored(t *testing.T) {
	// A recorded name not present in the registry (e.g. a removed script) must
	// not break the diff; it is simply neither pending nor re-run.
	withScripts(t, fakeWidget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB
	require.NoError(t, checkMigrationsTable(db))
	require.NoError(t, db.Create(&Migration{Name: "ghost_script", Batch: 1}).Error)

	names, err := getUnExecutedMigrationsScript(db)
	require.NoError(t, err)
	assert.Equal(t, []string{"fakeWidget"}, names)
}

func TestGetUnExecutedMigrationsScript_DBError(t *testing.T) {
	withScripts(t, fakeWidget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB
	// The migrations table does not exist -> Find must surface an error.
	names, err := getUnExecutedMigrationsScript(db)
	assert.Error(t, err)
	assert.Nil(t, names)
}

// ---------------------------------------------------------------------------
// Up — happy path
// ---------------------------------------------------------------------------

func TestUp_ExecutesAllAndAssignsSameBatch(t *testing.T) {
	withScripts(t, fakeWidget{}, fakeGadget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	require.NoError(t, Up(db))

	// Both tables created (script Up ran).
	assert.True(t, db.Migrator().HasTable("fake_widgets"))
	assert.True(t, db.Migrator().HasTable("fake_gadgets"))

	// Two records, both in batch 1.
	assert.Equal(t, int64(2), countMigrations(t, db))
	var recs []*Migration
	require.NoError(t, db.Order("id ASC").Find(&recs).Error)
	require.Len(t, recs, 2)
	assert.Equal(t, []string{"fakeWidget", "fakeGadget"}, migrationNames(t, db),
		"records must be written in registration order")
	for _, r := range recs {
		assert.Equal(t, 1, r.Batch, "a single Up() run assigns one shared batch number")
	}
}

func TestUp_IdempotentReRun(t *testing.T) {
	withScripts(t, fakeWidget{}, fakeGadget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	require.NoError(t, Up(db))
	require.Equal(t, int64(2), countMigrations(t, db))

	// Running Up again must be a no-op: no new records, same batch.
	require.NoError(t, Up(db))
	assert.Equal(t, int64(2), countMigrations(t, db))

	// And the batch number is NOT bumped to 2.
	batch, err := getLastBatchNum(db)
	require.NoError(t, err)
	assert.Equal(t, 1, batch)
}

func TestUp_IncrementsBatchAcrossRuns(t *testing.T) {
	withScripts(t, fakeWidget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	require.NoError(t, Up(db))
	require.Equal(t, int64(1), countMigrations(t, db))

	// Register a new script to simulate a later release and run Up again.
	withScripts(t, fakeWidget{}, fakeGadget{})
	require.NoError(t, Up(db))
	require.Equal(t, int64(2), countMigrations(t, db))

	// The new run must use batch 2 (lastBatch + 1).
	var gadget Migration
	require.NoError(t, db.Where("name = ?", "fakeGadget").First(&gadget).Error)
	assert.Equal(t, 2, gadget.Batch)
}

func TestUp_NoMigrationsIsNoOp(t *testing.T) {
	// Registry already fully applied -> "No migrations to execute".
	withScripts(t, fakeWidget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB
	require.NoError(t, checkMigrationsTable(db))
	require.NoError(t, db.Create(&Migration{Name: "fakeWidget", Batch: 1}).Error)

	require.NoError(t, Up(db))
	assert.Equal(t, int64(1), countMigrations(t, db), "nothing new should be recorded")
}

func TestUp_EmptyRegistryIsNoOp(t *testing.T) {
	withScripts(t) // no scripts at all

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	require.NoError(t, Up(db))
	assert.Equal(t, int64(0), countMigrations(t, db))
	assert.True(t, db.Migrator().HasTable(&Migration{}), "migrations table still auto-created")
}

// ---------------------------------------------------------------------------
// Up — error paths & the "registered name with no implementation" guard
// ---------------------------------------------------------------------------

// fakeFailingUp always errors from its Up(), letting us assert the engine
// propagates the failure and stops.
type fakeFailingUp struct{ failMsg string }

func (s fakeFailingUp) Up(db *gorm.DB) error {
	return errors.New(s.failMsg)
}
func (fakeFailingUp) Down(db *gorm.DB) error { return nil }

func TestUp_ScriptFailureStopsAndReturnsError(t *testing.T) {
	withScripts(t, fakeWidget{}, fakeFailingUp{failMsg: "boom"}, fakeGadget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	err = Up(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")

	// The failing script must NOT have been recorded, and execution stopped:
	// fakeGadget (after the failure) must not have run.
	var n int64
	db.Model(&Migration{}).Where("name = ?", "fakeGadget").Count(&n)
	assert.Equal(t, int64(0), n, "scripts after a failure must not run")

	// Only the script before the failure (fakeWidget) is recorded.
	// Note: sqlexec memory backend does not roll back DDL; the engine contract
	// is only that it returns an error. We assert the error, not the row.
}

// fakeUnregisteredName simulates a migration record whose name has no matching
// script implementation (e.g. a renamed/removed script). The engine must skip
// it with a warning rather than failing.
func TestUp_ContinuesWhenRegisteredNameHasNoImplementation(t *testing.T) {
	// The registry contains a name that will show up as pending (nothing
	// recorded for it), but we also seed a record for a *different* name not in
	// the registry to exercise the "not found in registry" branch.
	//
	// To exercise that branch directly we craft the scenario:
	//   - registry: [fakeWidget]
	//   - records : [ghost]   (recorded but not registered)
	// Up sees [fakeWidget] pending; ghost is neither pending nor re-run.
	// To force the "scriptType, ok := allScripts[name]; !ok" branch we need a
	// *pending* name absent from the registry. That cannot happen with the
	// current getUnExecutedMigrationsScript (which derives pending names FROM
	// the registry), so we test the symmetric Down path instead (see
	// TestDown_RemovesOrphanRecordWhenScriptMissing) which DOES hit the branch.
	t.Skip("covered by TestDown_RemovesOrphanRecordWhenScriptMissing")
}

// ---------------------------------------------------------------------------
// Down — happy path
// ---------------------------------------------------------------------------

func TestDown_RollsBackLastBatchInIDOrder(t *testing.T) {
	withScripts(t, fakeWidget{}, fakeGadget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	// Run Up to populate batch 1.
	require.NoError(t, Up(db))
	require.Equal(t, int64(2), countMigrations(t, db))

	// Roll back the last (only) batch.
	require.NoError(t, Down(db))

	// Both records removed.
	assert.Equal(t, int64(0), countMigrations(t, db))

	// Scripts were rolled back: tables dropped (in id DESC order, i.e. the
	// later-registered fakeGadget first, then fakeWidget).
	assert.False(t, db.Migrator().HasTable("fake_gadgets"))
	assert.False(t, db.Migrator().HasTable("fake_widgets"))
}

func TestDown_OnlyAffectsLastBatch(t *testing.T) {
	// Simulate two batches across two separate Up() runs.
	withScripts(t, fakeWidget{})
	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	require.NoError(t, Up(db)) // batch 1: fakeWidget
	withScripts(t, fakeWidget{}, fakeGadget{})
	require.NoError(t, Up(db)) // batch 2: fakeGadget

	require.Equal(t, int64(2), countMigrations(t, db))

	// Down must remove only batch 2 (fakeGadget).
	require.NoError(t, Down(db))
	assert.Equal(t, int64(1), countMigrations(t, db))

	var widget, gadget Migration
	require.NoError(t, db.Where("name = ?", "fakeWidget").First(&widget).Error)
	assert.Equal(t, 1, widget.Batch, "batch 1 must survive")
	err = db.Where("name = ?", "fakeGadget").First(&gadget).Error
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound, "batch 2 must be removed")

	// fakeGadget table dropped, fakeWidget table still present.
	assert.False(t, db.Migrator().HasTable("fake_gadgets"))
	assert.True(t, db.Migrator().HasTable("fake_widgets"))

	// batch 1 is now the last again.
	batch, err := getLastBatchNum(db)
	require.NoError(t, err)
	assert.Equal(t, 1, batch)
}

func TestDown_NoOpWhenNothingMigrated(t *testing.T) {
	withScripts(t, fakeWidget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB
	require.NoError(t, checkMigrationsTable(db))

	require.NoError(t, Down(db)) // lastBatch == 0 -> early return
	assert.Equal(t, int64(0), countMigrations(t, db))
}

// ---------------------------------------------------------------------------
// Down — error paths
// ---------------------------------------------------------------------------

// fakeFailingDown fails its Down() to assert the engine stops rolling back.
type fakeFailingDown struct{}

func (fakeFailingDown) Up(db *gorm.DB) error {
	return db.Migrator().AutoMigrate(&widgetModel{})
}
func (fakeFailingDown) Down(db *gorm.DB) error {
	return errors.New("cannot roll back")
}

func TestDown_ScriptFailureReturnsError(t *testing.T) {
	withScripts(t, fakeFailingDown{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	require.NoError(t, Up(db))
	require.Equal(t, int64(1), countMigrations(t, db))

	err = Down(db)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot roll back")
}

// TestDown_RemovesOrphanRecordWhenScriptMissing hits the branch where a
// recorded migration name has no matching script in the registry (e.g. the
// script was removed in a later release). The engine must delete the orphan
// record and continue, not fail.
func TestDown_RemovesOrphanRecordWhenScriptMissing(t *testing.T) {
	// Seed a migration record for a name that is NOT in the current registry,
	// but make it the last batch so Down() targets it.
	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB
	require.NoError(t, checkMigrationsTable(db))
	require.NoError(t, db.Create(&Migration{Name: "ghost_script", Batch: 3}).Error)
	require.Equal(t, int64(1), countMigrations(t, db))

	// Empty registry -> ghost_script has no implementation.
	withScripts(t)

	require.NoError(t, Down(db))
	assert.Equal(t, int64(0), countMigrations(t, db),
		"orphan record with no script implementation must be deleted and not fail")
}

// ---------------------------------------------------------------------------
// Up + Down round-trip
// ---------------------------------------------------------------------------

func TestUpThenDownThenUpRoundTrip(t *testing.T) {
	withScripts(t, fakeWidget{}, fakeGadget{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	require.NoError(t, Up(db))
	require.Equal(t, int64(2), countMigrations(t, db))
	require.True(t, db.Migrator().HasTable("fake_widgets"))

	require.NoError(t, Down(db))
	require.Equal(t, int64(0), countMigrations(t, db))
	require.False(t, db.Migrator().HasTable("fake_widgets"))

	// After a full rollback, batch resets to 0 and a fresh Up starts at batch 1.
	batch, err := getLastBatchNum(db)
	require.NoError(t, err)
	assert.Equal(t, 0, batch)

	require.NoError(t, Up(db))
	require.Equal(t, int64(2), countMigrations(t, db))
	batch, err = getLastBatchNum(db)
	require.NoError(t, err)
	assert.Equal(t, 1, batch, "batch numbering restarts at 1 after full rollback")
}

// ---------------------------------------------------------------------------
// Behavioral contract: Down rollbacks in reverse registration order.
//
// fakeOrdering scripts append their name to a shared slice on both Up and Down
// so the test can assert the exact ordering observed by the engine.
// ---------------------------------------------------------------------------

var orderingTrace atomic.Value // holds []string

func recordOrder(name string) {
	cur, _ := orderingTrace.Load().([]string)
	orderingTrace.Store(append(cur, name))
}

type orderA struct{}
type orderB struct{}
type orderC struct{}

func (orderA) Up(*gorm.DB) error   { recordOrder("UpA"); return nil }
func (orderA) Down(*gorm.DB) error { recordOrder("DownA"); return nil }
func (orderB) Up(*gorm.DB) error   { recordOrder("UpB"); return nil }
func (orderB) Down(*gorm.DB) error { recordOrder("DownB"); return nil }
func (orderC) Up(*gorm.DB) error   { recordOrder("UpC"); return nil }
func (orderC) Down(*gorm.DB) error { recordOrder("DownC"); return nil }

func TestDown_RollbackOrderIsReverseOfRegistration(t *testing.T) {
	withScripts(t, orderA{}, orderB{}, orderC{})

	td, err := testutil.NewIsolatedTestDB(t)
	require.NoError(t, err)
	db := td.GormDB

	orderingTrace.Store([]string{})
	require.NoError(t, Up(db))
	require.NoError(t, Down(db))

	got, _ := orderingTrace.Load().([]string)
	assert.Equal(t,
		[]string{"UpA", "UpB", "UpC", "DownC", "DownB", "DownA"},
		got,
		"Down must roll back in reverse registration order (id DESC)",
	)
}
