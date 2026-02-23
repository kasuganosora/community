# Complement Test Suite - Remaining Failures

**Date:** 2026-02-23
**Status:** 95 PASS / 5 FAIL (out of 100 top-level tests)

---

## 1. TestSyncTimelineGap

**Subtests:** `incremental`, `full`

**Root Cause:** When 50+ events arrive via federation (through `get_missing_events` backfill), the `/sync` endpoint returns all events in a single batch with `limited: false`. The test expects `limited: true` (timeline gap) because there are more events than the sync filter's timeline limit (20).

**Why it fails:**
- The backfilled events are stored but the sync query `created_at > sinceTime` fetches them all since they were all just created (their `created_at` timestamps are nearly identical, all after the sync token).
- The sync code does check `len(events) > timelineLimit` and sets `limited = true`, but the events may not be getting fetched correctly or the event ordering is inconsistent.
- Events appear in reverse/inconsistent order in the timeline.

**Fix needed:**
- Ensure backfilled events are ordered by `depth` or `origin_server_ts` consistently.
- Verify the `timelineLimit + 1` cap logic works correctly with `created_at` based queries.
- The reference hash computation may not match gomatrixserverlib exactly, causing event ID mismatches (the test looks for a specific event ID).

---

## 2. TestDeviceListUpdates

**Subtests:** `when_remote_user_leaves_a_room`, `when_leaving_a_room_with_a_remote_user`, `when_remote_user_rejoins_a_room`

**Root Cause:** When a remote user leaves a room via federation (`send_leave`), the leave membership event does not appear in `/sync` for remaining local users. The sync response returns `"rooms": {}` — the room entry is completely absent.

**Why it fails:**
- The `handleSendLeave` handler likely stores the leave event but the sync query may not pick it up.
- The `created_at` based incremental sync query might miss the event if there's a timing issue.
- The device list update logic depends on seeing the leave event in sync first.

**Fix needed:**
- Verify `handleSendLeave` stores the membership event in `matrix_events` (not just `matrix_room_members`).
- Ensure the leave event's `created_at` is after the sync token so it appears in incremental sync.
- Check that `getDeviceListUpdates` properly handles remote user departures.

---

## 3. TestMessagesOverFederation

**Subtests:** `Visible_shared_history_after_joining_new_room_(backfill)`, `Visible_shared_history_after_re-joining_room_(backfill)`

**Root Cause:** After joining a room with 300 historical messages on a remote server, the `/messages` endpoint only returns ~100 events instead of all 300. The on-demand backfill via `tryBackfillFromRemote` only fetches one batch.

**Why it fails:**
- The `FetchBackfill` call uses a default limit of 100 events per request.
- The backfill does not iterate to fetch ALL historical events — it makes a single request and stops.
- The `/backfill` federation endpoint on the remote server may also cap responses.

**Fix needed:**
- Implement iterative backfill: after fetching a batch, check if more events are available and fetch additional batches until the requested limit is met or no more events are returned.
- Increase the per-request limit or make multiple requests with the earliest fetched event as the new starting point.

---

## 4. TestPushRuleRoomUpgrade

**Subtests:** `joining_a_remote_manually_upgraded_room_carries_over_existing_push_rules`, `manually_upgrading_a_room_carries_over_existing_push_rules_for_local_users`

**Root Cause:** Room creation with `creation_content` containing a `predecessor` field returns 400 Internal Error. This prevents the "manual room upgrade" flow from working, which in turn prevents push rule migration from being triggered.

**Why it fails:**
- The `CreateRoom` handler doesn't properly handle the `predecessor` field in `creation_content`.
- When `creation_content` is provided, it may conflict with the auto-generated `m.room.create` event content.
- The 400 error cascades — if the new room can't be created, push rules are never migrated.

**Fix needed:**
- Update `CreateRoom` in `room.go` to merge `creation_content` (including `predecessor`) into the `m.room.create` event.
- Ensure the predecessor field doesn't cause validation errors.
- Test that the tombstone → push rule migration path works end-to-end.

---

## 5. TestSync (partial failure)

**Subtests:** Federation-related redaction subtest

**Root Cause:** Related to TestSyncTimelineGap — federation events not appearing correctly in sync responses.

**Why it fails:**
- Same underlying issue with federation event storage and sync query timing.

**Fix needed:**
- Same as TestSyncTimelineGap fixes.

---

## Architecture Notes

These 5 failures all relate to **federation event flow → sync delivery**:

```
Remote Server → PUT /send/{txnId} → storeFederationPDU()
                                          ↓
                    get_missing_events (gap detection) → storeFederationPDU()
                                          ↓
                              matrix_events table
                                          ↓
                    /sync (created_at > sinceTime query) → timeline response
```

Key architectural issues:
1. **Event ordering**: Backfilled events have `created_at ≈ now` but `origin_server_ts` from the past. Sync uses `created_at` for filtering but `origin_server_ts` for ordering, creating inconsistencies.
2. **Backfill depth**: Single-batch backfill is insufficient for rooms with long history.
3. **Reference hash**: May not match gomatrixserverlib's implementation exactly, causing event ID mismatches in tests that check specific IDs.
4. **Room creation**: `creation_content` merging is incomplete.
