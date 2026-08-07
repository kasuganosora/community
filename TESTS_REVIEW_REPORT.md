# Deep Code Review & Fixes: tests/integration/ and tests/testutil/

## Scope

Reviewed every Go file under `tests/integration/` and `tests/testutil/`
(including all subdirectories), plus the dependencies they touch
(`db/redis/redis.go`, `db/redis/testutil.go`, `utils/string.go`) and the
consumers of these helpers (`feed/integration_test.go`).

Files reviewed:
- `tests/integration/oauth_integration_test.go`
- `tests/testutil/db_mock.go`
- `tests/testutil/db_mock_test.go`
- `tests/testutil/env.go`
- `tests/testutil/fixture.go`
- `tests/testutil/redis_helper.go`
- `tests/testutil/redis_mock.go`
- `tests/testutil/redis_mock_test.go`
- `tests/testutil/setup.go`
- `tests/testutil/setup_test.go` (new — regression tests)

---

## Issues Found & Fixed

### CRITICAL

#### C1. `NewTestSetup` permanently mutated global `db/redis.redises` and never restored it
**File:** `tests/testutil/setup.go` (lines ~129-143)

`cRedis.SetRedisesForTest([]*goredis.Client{setup.Redis.Client})` overwrites
the package-level `redises` slice that `db.GetRedis()`, `feed`, `cache`,
`middleware`, `streaming`, and `server` all depend on. The original code never
restored the previous value. After the test, `Close()` closes the injected
client, so the global pool was left pointing at a **closed** client — every
subsequent `GetRedis()` call in the same process would use a dead connection.
With `t.Parallel()`, two setups would also data-race on the global.

**Fix:** Snapshot `prevRedises := cRedis.GetRedisesForTest()` before injecting,
and restore it in `t.Cleanup` after closing the client. This makes the global
mutation test-local and race-free.

**Regression test added:** `TestNewTestSetup_RestoresGlobalRedis` and
`TestNewTestSetup_DoesNotLeaveDeadClient` in `setup_test.go`.

---

#### C2. `NewRealRedis` silently dropped `FlushDB` errors and reused an about-to-be-cancelled context
**File:** `tests/testutil/redis_mock.go` (line ~69)

`client.FlushDB(ctx)` used the same `ctx` whose `cancel()` was deferred right
after the ping, and the returned error was discarded. A failed flush silently
left stale data, causing cross-test bleed.

**Fix:** Use a fresh `flushCtx` with its own timeout/cancel, and log flush
failures via `t.Logf` so the cause is diagnosable (non-fatal, since flush is a
best-effort isolation aid).

---

### HIGH

#### H1. `SetupSharedTestDB` returned a borrowed view whose `Close()` destroyed the shared singleton
**File:** `tests/testutil/db_mock.go` (`SetupSharedTestDB`, `Close`)

The `*TestDB` returned by `SetupSharedTestDB` shared the singleton's `DB` and
`GormDB`. Calling `Close()` on it (e.g. via `TestSetup.Close()` or a caller's
defer) invoked `t.DB.Close()`, tearing down the **shared** database for every
other test.

**Fix:** Added an unexported `ownsDB` field to `TestDB`. Independent
constructors (`NewTestDB`, `NewIsolatedTestDB`, `newTestDBNoCleanup`) set
`ownsDB: true`. The shared-borrow view from `SetupSharedTestDB` leaves it
`false`. `Close()` now only closes the underlying `*api.DB` when `ownsDB` is
true (and only closes the session otherwise). `Close()` is also now idempotent
(nil-guarded) so double-close is safe.

**Regression test added:** `TestTestDB_SharedBorrowDoesNotCloseUnderlying` and
`TestTestDB_CloseIsIdempotent`.

---

#### H4. `TestSetup.Close()` was not idempotent and verified mock expectations after test completion
**File:** `tests/testutil/setup.go` (`Close`)

`Close()` called `s.Redis.AssertExpectations(s.t)`, but `Close()` can run from
`t.Cleanup` *or* be invoked manually by a caller — in the latter case the test
may already be done, making `t.Errorf` a confusing no-op. Also, double-close
(manual + cleanup) was possible.

**Fix:** Made `Close()` idempotent via a `closed` flag. Moved
`AssertExpectations` out of `Close()` and into the `t.Cleanup` registered by
`NewTestSetup`, where the test is guaranteed still active. Documented this in a
comment on `Close()`.

---

### MEDIUM

#### M2. `TestNewAutoRedis_WhenRedisUnavailable` never verified mock expectations
**File:** `tests/testutil/redis_mock_test.go`

The test set up `client.Mock.ExpectPing()` but never asserted expectations were
met — a missed expectation would pass silently.

**Fix:** Added a `t.Cleanup` that calls `client.AssertExpectations(t)`.

---

## Items Reviewed and Deliberately Left Unchanged

- **`fixture.go` `Create*` methods using `t.Fatalf`:** Correct for helpers
  invoked on the test goroutine; `t.Helper()` is already set. No change.
- **`fixture.go` `PtrString`/`PtrInt`/`PtrInt64`/`PtrTime` using `new(value)`:**
  Verified via experimentation that Go 1.26 `new(value)` allocates and
  initializes with a **copy** of the value (distinct allocation, value
  preserved). These are functionally correct (if unconventional). Left as-is to
  avoid cosmetic churn outside the review's scope.
- **`env.go` `loadEnvFile` mutating process env:** Intentional, guarded by
  `sync.Once`, only runs once per process. Acceptable.
- **`integration/oauth_integration_test.go`:** Tests are minimal (assert
  request well-formedness without a real server), but not incorrect. The
  discarded `*gin.Engine` from `gin.CreateTestContext` is idiomatic. The
  `init()` gin mode setting is process-global and harmless. Left as-is.
- **`redis_helper.go` `IsRedisAvailable`/`FlushTestRedis`:** Correct; context
  lifetime and close handling are sound.
- **`db_mock.go` `GetSharedTestDB` singleton:** Intentional singleton pattern;
  `sync.Once` provides the necessary synchronization. `errSharedDB` is written
  only inside `Once.Do`, so the happens-before is guaranteed.

---

## Verification

- `go build ./tests/...` — passes
- `go build ./...` — passes (full repo, no consumers broken)
- `go vet ./tests/... ./feed/...` — passes
- `gofmt -l tests/...` — no diffs
- `go test -race -count=1 ./tests/...` — all pass
- `go test -race -count=3 -shuffle=on ./tests/testutil/` — stable (no flakiness
  under random ordering, confirming the global-state restore is robust)

No public API signatures were changed. The new `ownsDB` field and `closed`
field are unexported. New regression tests were added in `setup_test.go`.
