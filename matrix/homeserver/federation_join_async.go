package homeserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Partial-state room joins
//
// A send_join answer for a huge room carries tens of thousands of member and
// historical state events. Storing all of them inside the shared join budget
// (MATRIX_JOIN_TOTAL_TIMEOUT) is what made big rooms fail with a 504, so a join
// is persisted in two stages once it has been fully validated:
//
//	STAGE 1 (synchronous, one transaction, inside the join budget):
//	  the room row, our join event, the create/power_levels/join_rules/... key
//	  state, the joining user's own membership and the auth ancestors of all of
//	  those. This is enough to authorise and send events as the local user.
//	STAGE 2 (background, MATRIX_JOIN_BG_* settings): every remaining validated
//	  event - mainly the other members - is stored in batches. Writes are
//	  "insert if absent", so a re-run (retry, restart) skips what is stored and
//	  never overwrites newer local state.
//
// The room is flagged partial for the whole time between the stages. The flag
// reuses matrix_rooms_hs.current_state_id (otherwise unused), holding
// "partial|<join event id>|<server that served the join>", so no schema change
// is needed and the flag survives a crash. While it is set:
//   - federation endpoints that would serve or depend on the full state
//     (state, state_ids, event, make_join, send_join, make_leave, send_leave)
//     answer 404 M_NOT_FOUND "state is still being synchronised";
//   - PDUs pushed to us for the room are queued in memory (bounded) and
//     replayed once the flag is cleared;
//   - events we send are also delivered to the join server, because the member
//     list that normally decides destinations is still incomplete;
//   - client reads work on whatever is stored (the member list may be short).
//
// After a crash the flag is still set; ResumePartialStateJoins (called at
// start-up) and any later join request for the room re-fetch the room state
// from the recorded server (federation /state at the join event) and run the
// same idempotent writer.
type asyncJoinSettings struct {
	Workers       int           // rooms written concurrently
	MaxHeld       int           // rooms whose remaining events may be kept in memory at once
	BatchEvents   int           // events per transaction
	BatchBytes    int           // approximate PDU bytes per transaction
	Retries       int           // whole-job retries after the first attempt
	RetryBase     time.Duration // first retry delay, doubled each time (max 5 min)
	SyncThreshold int           // remaining events written inline when at most this many
	FetchTimeout  time.Duration // one /state fetch + validation on resume
	MaxPending    int           // queued inbound PDUs per partial room
}

func loadAsyncJoinSettings() asyncJoinSettings {
	return asyncJoinSettings{
		Workers:       envInt("MATRIX_JOIN_BG_WORKERS", 2, 1, 16),
		MaxHeld:       envInt("MATRIX_JOIN_BG_MAX_HELD", 4, 1, 64),
		BatchEvents:   envInt("MATRIX_JOIN_BG_BATCH", 500, 10, 5000),
		BatchBytes:    4 << 20,
		Retries:       envInt("MATRIX_JOIN_BG_RETRIES", 5, 0, 50),
		RetryBase:     envDuration("MATRIX_JOIN_BG_RETRY_DELAY", 5*time.Second),
		SyncThreshold: envInt("MATRIX_JOIN_SYNC_STATE_EVENTS", 2000, 0, 1_000_000),
		FetchTimeout:  envDuration("MATRIX_JOIN_BG_FETCH_TIMEOUT", 10*time.Minute),
		MaxPending:    envInt("MATRIX_JOIN_BG_MAX_PENDING_PDUS", 1000, 0, 100000),
	}
}

// asyncJoinSettingsSource is replaceable in tests.
var asyncJoinSettingsSource = loadAsyncJoinSettings

// ---- partial-state marker ---------------------------------------------------

const partialStatePrefix = "partial|"

var errRoomPartialState = errors.New("room state is still being synchronised on this server, try again later")

func partialStateMarker(joinEventID, server string) string {
	m := partialStatePrefix + joinEventID + "|" + server
	if len(m) > 255 { // column is varchar(255): the server is only a resume hint
		m = partialStatePrefix + joinEventID + "|"
	}
	return m
}

func parsePartialStateMarker(v *string) (joinEventID, server string, ok bool) {
	if v == nil || len(*v) < len(partialStatePrefix) || (*v)[:len(partialStatePrefix)] != partialStatePrefix {
		return "", "", false
	}
	rest := (*v)[len(partialStatePrefix):]
	for i := 0; i < len(rest); i++ {
		if rest[i] == '|' {
			return rest[:i], rest[i+1:], true
		}
	}
	return rest, "", true
}

// roomPartialState reports whether roomID is flagged partial. A missing room or
// a database error reads as "not partial".
func roomPartialState(tx *gorm.DB, roomID string) (joinEventID, server string, partial bool) {
	var room MatrixRoom
	if err := tx.Select("room_id", "current_state_id").Where("room_id = ?", roomID).First(&room).Error; err != nil {
		return "", "", false
	}
	return parsePartialStateMarker(room.CurrentStateID)
}

// ---- stage selection --------------------------------------------------------

var essentialStateTypes = map[string]bool{
	"m.room.create": true, "m.room.power_levels": true, "m.room.join_rules": true,
	"m.room.history_visibility": true, "m.room.guest_access": true, "m.room.name": true,
	"m.room.topic": true, "m.room.avatar": true, "m.room.canonical_alias": true,
	"m.room.encryption": true, "m.room.server_acl": true, "m.room.tombstone": true,
	"m.room.pinned_events": true,
}

// maxEssentialOtherState bounds how much non-member state of unknown types
// (space children, bridge data, ...) is stored synchronously.
const maxEssentialOtherState = 2000

// essentialEvents returns the IDs persisted in stage 1: everything the join's
// authorisation depends on, key room state, the joining user's membership, and
// the auth ancestors of those so the stored DAG stays closed.
func (pj *preparedFederationJoin) essentialEvents() map[string]bool {
	ess := make(map[string]bool)
	for id := range pj.critical {
		if _, ok := pj.events[id]; ok {
			ess[id] = true
		}
	}
	self := string(pj.join.SenderID())
	other := 0
	for _, p := range pj.state {
		switch t := p.Type(); {
		case essentialStateTypes[t]:
			ess[p.EventID()] = true
		case t == "m.room.member":
			if p.StateKey() != nil && *p.StateKey() == self {
				ess[p.EventID()] = true
			}
		default:
			other++
		}
	}
	if other <= maxEssentialOtherState {
		for _, p := range pj.state {
			if p.Type() != "m.room.member" {
				ess[p.EventID()] = true
			}
		}
	}
	todo := make([]string, 0, len(ess))
	for id := range ess {
		todo = append(todo, id)
	}
	for len(todo) > 0 {
		id := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		for _, parent := range pj.events[id].AuthEventIDs() {
			if _, ok := pj.events[parent]; ok && !ess[parent] {
				ess[parent] = true
				todo = append(todo, parent)
			}
		}
	}
	return ess
}

type joinRemainderItem struct {
	p     gmsl.PDU
	state bool // part of the room's current state: also needs state/member rows
}

// joinRemainder is the validated part of a join that is still to be stored.
// Written entries are zeroed so memory shrinks as the writer advances.
type joinRemainder struct {
	roomID  string
	server  string
	items   []joinRemainderItem
	next    int
	total   int
	written int
	refetch bool // items are not in memory: fetch them from the remote first
	// fullAfter: the join answer omitted members (MSC3706), so once items are
	// written the complete state must be fetched as well.
	fullAfter bool
}

// ---- servers known to be in a partial room ----------------------------------

// partialRoomServers remembers the servers_in_room hint of a members_omitted
// join answer until the room's state is complete. It is memory only (no schema
// change): after a restart the join server recorded in the partial marker is
// used, as before.
var partialRoomServers = struct {
	sync.Mutex
	rooms map[string][]string
}{rooms: map[string][]string{}}

// maxPartialFanout bounds how many hinted servers an outgoing event is sent to
// (besides the join server) while the real member list is still incomplete.
func maxPartialFanout() int { return envInt("MATRIX_JOIN_PARTIAL_FANOUT", 8, 0, 200) }

func rememberPartialRoomServers(roomID string, servers []string) {
	partialRoomServers.Lock()
	defer partialRoomServers.Unlock()
	if len(servers) == 0 {
		delete(partialRoomServers.rooms, roomID)
		return
	}
	if len(partialRoomServers.rooms) >= 256 { // never grow without bound
		for k := range partialRoomServers.rooms {
			delete(partialRoomServers.rooms, k)
			break
		}
	}
	partialRoomServers.rooms[roomID] = servers
}

// partialRoomServerHints returns at most limit remembered servers for roomID.
func partialRoomServerHints(roomID string, limit int) []string {
	partialRoomServers.Lock()
	defer partialRoomServers.Unlock()
	list := partialRoomServers.rooms[roomID]
	if len(list) > limit {
		list = list[:limit]
	}
	return append([]string(nil), list...)
}

func forgetPartialRoomServers(roomID string) {
	partialRoomServers.Lock()
	delete(partialRoomServers.rooms, roomID)
	partialRoomServers.Unlock()
}

// persistFederationJoinStaged runs stage 1. It returns the events left for
// stage 2 (nil when everything was stored) and flags the room partial in the
// same transaction when there are any.
func (r *RoomService) persistFederationJoinStaged(ctx context.Context, pj *preparedFederationJoin, server string) (*joinRemainder, error) {
	essential := pj.essentialEvents()
	stateIDs := make(map[string]bool, len(pj.state))
	for _, p := range pj.state {
		stateIDs[p.EventID()] = true
	}
	var rem *joinRemainder
	room := MatrixRoom{RoomID: pj.roomID, RoomVersion: pj.version, Creator: pj.creator}
	err := db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rem = nil
		if err := tx.Where("room_id = ?", pj.roomID).FirstOrCreate(&room).Error; err != nil {
			return err
		}
		essentialPDUs := make([]gmsl.PDU, 0, len(essential))
		for id, p := range pj.events {
			if essential[id] {
				essentialPDUs = append(essentialPDUs, p)
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := persistVerifiedPDUsBulk(tx, r.HS, essentialPDUs, false, false, func(f func(*gorm.DB) error) error { return f(tx) }); err != nil {
			return err
		}
		for _, p := range pj.state {
			if !essential[p.EventID()] {
				continue
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			e := pduToMatrixEvent(p)
			r.updateRoomState(tx, pj.roomID, e)
			if e.EventType == EventTypeRoomMember {
				m, _ := e.Content["membership"].(string)
				r.updateRoomMember(tx, pj.roomID, *e.StateKey, MembershipState(m), e.EventID)
			}
		}
		if err := persistVerifiedPDUAtState(tx, r.HS, pj.join, true, pj.state, true); err != nil {
			return err
		}
		items := make([]joinRemainderItem, 0, len(pj.events)-len(essential))
		for id, p := range pj.events {
			if !essential[id] {
				items = append(items, joinRemainderItem{p: p, state: stateIDs[id]})
			}
		}
		if len(items) == 0 && !pj.membersOmitted {
			return nil
		}
		rem = &joinRemainder{roomID: pj.roomID, server: server, items: items, total: len(items), fullAfter: pj.membersOmitted}
		return tx.Model(&MatrixRoom{}).Where("room_id = ?", pj.roomID).
			Update("current_state_id", partialStateMarker(pj.join.EventID(), server)).Error
	})
	if err != nil {
		return nil, err
	}
	if rem != nil && pj.membersOmitted {
		rememberPartialRoomServers(pj.roomID, pj.serversInRoom)
	}
	return rem, nil
}

// ---- stage 2 writer ---------------------------------------------------------

// joinRemainderBatchHook runs before every batch write when set (tests); a
// non-nil error fails that attempt.
var joinRemainderBatchHook func() error

// runJoinRemainder stores the remaining events in bounded batches, resuming at
// rem.next. Each batch is its own transaction and is idempotent.
func (r *RoomService) runJoinRemainder(ctx context.Context, rem *joinRemainder, st asyncJoinSettings) error {
	ctx = withCPUThrottle(ctx)
	started := time.Now()
	lastLog := time.Now()
	for rem.next < len(rem.items) {
		if err := ctx.Err(); err != nil {
			return err
		}
		end, size := rem.next, 0
		for end < len(rem.items) && end-rem.next < st.BatchEvents && (size < st.BatchBytes || end == rem.next) {
			size += len(rem.items[end].p.JSON())
			end++
		}
		batch := rem.items[rem.next:end]
		var err error
		for try := 0; try < 3; try++ {
			if joinRemainderBatchHook != nil {
				err = joinRemainderBatchHook()
			}
			if err == nil {
				err = r.writeJoinBatch(ctx, rem.roomID, batch)
			}
			if err == nil || ctx.Err() != nil {
				break
			}
			select {
			case <-time.After(time.Duration(try+1) * 200 * time.Millisecond):
			case <-ctx.Done():
			}
		}
		if err != nil {
			return err
		}
		for i := range batch {
			batch[i] = joinRemainderItem{}
		}
		rem.written += len(batch)
		rem.next = end
		throttleTick(ctx)
		if time.Since(lastLog) >= 10*time.Second {
			lastLog = time.Now()
			log.Info("matrix join %s: stored %d/%d remaining state events", rem.roomID, rem.written, rem.total)
		}
	}
	rem.items = nil
	log.Debug("matrix join %s: wrote %d remaining state events in %s", rem.roomID, rem.written, time.Since(started).Round(time.Millisecond))
	return nil
}

func (r *RoomService) writeJoinBatch(ctx context.Context, roomID string, batch []joinRemainderItem) error {
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		events := make([]*MatrixEvent, len(batch))
		ids := make([]string, len(batch))
		for i, it := range batch {
			events[i] = pduToMatrixEvent(it.p)
			ids[i] = events[i].EventID
		}
		var have []string
		if err := tx.Model(&MatrixEvent{}).Where("event_id IN ?", ids).Pluck("event_id", &have).Error; err != nil {
			return err
		}
		stored := make(map[string]bool, len(have))
		for _, id := range have {
			stored[id] = true
		}
		fresh := make([]*MatrixEvent, 0, len(batch))
		for _, e := range events {
			if !stored[e.EventID] {
				fresh = append(fresh, e)
			}
		}
		if len(fresh) > 0 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(fresh, 100).Error; err != nil {
				return err
			}
		}
		byType := map[EventType][]*MatrixEvent{}
		for i, it := range batch {
			if it.state && events[i].StateKey != nil {
				byType[events[i].EventType] = append(byType[events[i].EventType], events[i])
			}
		}
		for kind, list := range byType {
			keys := make([]string, len(list))
			for i, e := range list {
				keys[i] = *e.StateKey
			}
			var haveKeys []string
			if kind == EventTypeRoomMember {
				if err := tx.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id IN ?", roomID, keys).Pluck("user_id", &haveKeys).Error; err != nil {
					return err
				}
				present := toSet(haveKeys)
				members := make([]*MatrixRoomMember, 0, len(list))
				for _, e := range list {
					m, _ := e.Content["membership"].(string)
					if m != "" && !present[*e.StateKey] {
						members = append(members, &MatrixRoomMember{RoomID: roomID, UserID: *e.StateKey, Membership: MembershipState(m), EventID: e.EventID})
					}
				}
				if len(members) > 0 {
					if err := tx.CreateInBatches(members, 200).Error; err != nil {
						return err
					}
				}
			}
			if err := tx.Model(&MatrixRoomState{}).Where("room_id = ? AND event_type = ? AND state_key IN ?", roomID, kind, keys).Pluck("state_key", &haveKeys).Error; err != nil {
				return err
			}
			present := toSet(haveKeys)
			rows := make([]*MatrixRoomState, 0, len(list))
			for _, e := range list {
				if !present[*e.StateKey] {
					rows = append(rows, &MatrixRoomState{RoomID: roomID, EventType: kind, StateKey: *e.StateKey, Content: e.Content, EventID: e.EventID})
				}
			}
			if len(rows) > 0 {
				if err := tx.CreateInBatches(rows, 200).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func toSet(list []string) map[string]bool {
	out := make(map[string]bool, len(list))
	for _, s := range list {
		out[s] = true
	}
	return out
}

// finishPartialJoin clears the partial flag, replays PDUs queued meanwhile and
// wakes clients.
func (r *RoomService) finishPartialJoin(roomID string) {
	res := db.GetDB().Model(&MatrixRoom{}).Where("room_id = ? AND current_state_id LIKE ?", roomID, partialStatePrefix+"%").
		Update("current_state_id", nil)
	if res.Error != nil {
		log.Warn("matrix join %s: clearing partial-state flag failed: %v", roomID, res.Error)
		return
	}
	forgetPartialRoomServers(roomID)
	queued := takePartialPDUs(roomID)
	replayed := 0
	for _, ev := range queued {
		if _, err := storeFederationPDU(r.HS, ev); err == nil {
			replayed++
		}
	}
	if len(queued) > 0 {
		log.Info("matrix join %s: replayed %d of %d PDUs received during state sync", roomID, replayed, len(queued))
	}
	go r.HS.NotifyRoomEvent(roomID)
}

// ---- inbound PDU queue for partial rooms ------------------------------------

var partialPDUQueue = struct {
	sync.Mutex
	rooms map[string][]map[string]any
}{rooms: map[string][]map[string]any{}}

// queuePartialPDU keeps an inbound PDU for replay; false when the queue is full.
func queuePartialPDU(roomID string, event map[string]any) bool {
	limit := asyncJoinSettingsSource().MaxPending
	partialPDUQueue.Lock()
	defer partialPDUQueue.Unlock()
	if len(partialPDUQueue.rooms[roomID]) >= limit {
		return false
	}
	partialPDUQueue.rooms[roomID] = append(partialPDUQueue.rooms[roomID], event)
	return true
}

func takePartialPDUs(roomID string) []map[string]any {
	partialPDUQueue.Lock()
	defer partialPDUQueue.Unlock()
	out := partialPDUQueue.rooms[roomID]
	delete(partialPDUQueue.rooms, roomID)
	return out
}

// finishOrDeferJoinRemainder stores a small remainder inline (within ctx) and
// otherwise, or when the inline write fails, continues in the background. It
// returns how many events are still pending when it returns.
func (r *RoomService) finishOrDeferJoinRemainder(ctx context.Context, rem *joinRemainder) int {
	st := asyncJoinSettingsSource()
	if len(rem.items) <= st.SyncThreshold && !rem.fullAfter {
		err := r.runJoinRemainder(ctx, rem, st)
		if err == nil {
			r.finishPartialJoin(rem.roomID)
			return 0
		}
		log.Warn("matrix join %s: storing remaining state inline failed, continuing in background: %v", rem.roomID, err)
	}
	pending := len(rem.items) - rem.next
	r.scheduleJoinRemainder(rem)
	return pending
}

// ---- job scheduling ---------------------------------------------------------

type joinJobs struct {
	mu      sync.Mutex
	running map[string]bool
	held    int
	sem     chan struct{}
	semCap  int
	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	// per-room cancel and completion, so leaving a room can stop its state sync
	cancels map[string]context.CancelFunc
	done    map[string]chan struct{}
}

// cancelRoom stops the background state sync of one room and waits (at most
// wait) for it to finish. It is how leaving a partial-state room cancels its job.
func (j *joinJobs) cancelRoom(roomID string, wait time.Duration) {
	j.mu.Lock()
	cancel, done := j.cancels[roomID], j.done[roomID]
	j.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if done != nil {
		select {
		case <-done:
		case <-time.After(wait):
			log.Warn("matrix join %s: state sync did not stop within %s of the cancel", roomID, wait)
		}
	}
}

var asyncJoins = &joinJobs{running: map[string]bool{}}

// shutdown cancels running jobs and waits for them (tests and graceful stop).
func (j *joinJobs) shutdown() {
	j.mu.Lock()
	if j.cancel != nil {
		j.cancel()
	}
	j.mu.Unlock()
	j.wg.Wait()
	j.mu.Lock()
	j.ctx, j.cancel, j.running, j.held, j.sem, j.semCap = nil, nil, map[string]bool{}, 0, nil, 0
	j.cancels, j.done = nil, nil
	j.mu.Unlock()
}

// idle blocks until no job runs (tests).
func (j *joinJobs) idle() { j.wg.Wait() }

// scheduleJoinRemainder hands a room's remaining events to the background
// writer. A room that already has a job is left alone. When too many rooms
// already hold events in memory, this one drops them and refetches later.
func (r *RoomService) scheduleJoinRemainder(rem *joinRemainder) {
	st := asyncJoinSettingsSource()
	j := asyncJoins
	j.mu.Lock()
	if j.running[rem.roomID] {
		j.mu.Unlock()
		return
	}
	holding := len(rem.items) > 0
	if holding && j.held >= st.MaxHeld {
		log.Info("matrix join %s: background queue full, remaining state will be re-fetched", rem.roomID)
		rem.items, rem.next, rem.written, rem.refetch, holding = nil, 0, 0, true, false
	}
	if !holding {
		rem.refetch = true
	} else {
		j.held++
	}
	if j.ctx == nil {
		j.ctx, j.cancel = context.WithCancel(context.Background())
	}
	if j.sem == nil || j.semCap != st.Workers {
		j.sem, j.semCap = make(chan struct{}, st.Workers), st.Workers
	}
	j.running[rem.roomID] = true
	roomCtx, roomCancel := context.WithCancel(j.ctx)
	if j.cancels == nil {
		j.cancels, j.done = map[string]context.CancelFunc{}, map[string]chan struct{}{}
	}
	doneCh := make(chan struct{})
	j.cancels[rem.roomID], j.done[rem.roomID] = roomCancel, doneCh
	ctx, sem := roomCtx, j.sem
	j.wg.Add(1)
	j.mu.Unlock()
	go func() {
		defer j.wg.Done()
		defer close(doneCh)
		defer roomCancel()
		defer func() {
			j.mu.Lock()
			delete(j.running, rem.roomID)
			delete(j.cancels, rem.roomID)
			delete(j.done, rem.roomID)
			if holding {
				j.held--
			}
			j.mu.Unlock()
		}()
		select {
		case sem <- struct{}{}:
			defer func() { <-sem }()
		case <-ctx.Done():
			return
		}
		r.runJoinJob(ctx, rem, st)
	}()
}

func (r *RoomService) runJoinJob(ctx context.Context, rem *joinRemainder, st asyncJoinSettings) {
	started := time.Now()
	delay := st.RetryBase
	for attempt := 0; ; attempt++ {
		var err error
		if rem.refetch {
			if err = r.refetchJoinRemainder(ctx, rem, st); err == nil {
				rem.fullAfter = false // what was just fetched is the complete state
			}
		}
		if err == nil {
			err = r.runJoinRemainder(ctx, rem, st)
		}
		if err == nil && rem.fullAfter {
			// Members were omitted from the join answer: fetch the complete
			// state now. On a retry rem.refetch is still set, so it is not
			// fetched twice.
			rem.fullAfter, rem.refetch = false, true
			rem.items, rem.next, rem.written = nil, 0, 0
			if err = r.refetchJoinRemainder(ctx, rem, st); err == nil {
				err = r.runJoinRemainder(ctx, rem, st)
			}
		}
		if err == nil {
			r.finishPartialJoin(rem.roomID)
			log.Info("matrix join %s: state sync complete (%d events, %s)", rem.roomID, rem.written, time.Since(started).Round(time.Millisecond))
			return
		}
		if ctx.Err() != nil {
			log.Info("matrix join %s: state sync interrupted, resumes on next start", rem.roomID)
			return
		}
		log.Warn("matrix join %s: state sync attempt %d/%d failed after %d events: %v", rem.roomID, attempt+1, st.Retries+1, rem.written, err)
		if attempt >= st.Retries {
			log.Error("matrix join %s: state sync gave up; the room stays in partial state until the next start or join request", rem.roomID)
			return
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
		if delay *= 2; delay > 5*time.Minute {
			delay = 5 * time.Minute
		}
	}
}

// ---- resume -----------------------------------------------------------------

// ResumePartialStateJoins restarts the state sync of every room left partial,
// e.g. by a crash or restart during the background write.
func (r *RoomService) ResumePartialStateJoins() {
	var ids []string
	if err := db.GetDB().Model(&MatrixRoom{}).Where("current_state_id LIKE ?", partialStatePrefix+"%").Pluck("room_id", &ids).Error; err != nil {
		log.Warn("matrix: listing partial-state rooms failed: %v", err)
		return
	}
	for _, id := range ids {
		log.Info("matrix join %s: resuming interrupted state sync", id)
		r.scheduleJoinRemainder(&joinRemainder{roomID: id, refetch: true})
	}
}

// resumePartialJoinIfNeeded is called when somebody joins a room we already
// know: a partial room without a running job gets one.
func (r *RoomService) resumePartialJoinIfNeeded(roomID string) {
	if _, _, partial := roomPartialState(db.GetDB(), roomID); partial {
		r.scheduleJoinRemainder(&joinRemainder{roomID: roomID, refetch: true})
	}
}

// refetchJoinRemainder loads the room state at our join event from the server
// that served the join (or another candidate), validates it like a join
// response and fills rem with it. Already stored events are skipped later by
// the writer.
func (r *RoomService) refetchJoinRemainder(ctx context.Context, rem *joinRemainder, st asyncJoinSettings) error {
	joinEventID, server, partial := roomPartialState(db.GetDB(), rem.roomID)
	if !partial {
		rem.items, rem.refetch = nil, false
		return nil
	}
	var room MatrixRoom
	var joinRow MatrixEvent
	if err := db.GetDB().Where("room_id = ?", rem.roomID).First(&room).Error; err != nil {
		return fmt.Errorf("room row: %w", err)
	}
	if err := db.GetDB().Where("event_id = ?", joinEventID).First(&joinRow).Error; err != nil {
		return fmt.Errorf("stored join event: %w", err)
	}
	version := room.RoomVersion
	if version == "" {
		version = "10"
	}
	join, err := parseAndValidatePDU(version, []byte(joinRow.FederationJSON))
	if err != nil {
		return fmt.Errorf("stored join event: %w", err)
	}
	// The server that served the join first, then the usual candidate order.
	candidates := orderJoinServers(r.HS.Config.ServerName, rem.roomID, []string{server}, nil, "")
	if server != "" {
		candidates = append([]string{server}, removeString(candidates, server)...)
	}
	// servers_in_room from the join answer (memory only) come after the
	// join server and the room's own domain.
	for _, hinted := range partialRoomServerHints(rem.roomID, 20) {
		if !contains(candidates, hinted) {
			candidates = append(candidates, hinted)
		}
	}
	if len(candidates) > 6 {
		candidates = candidates[:6]
	}
	var lastErr error
	for _, srv := range candidates {
		fctx, cancel := context.WithTimeout(ctx, st.FetchTimeout)
		pj, err := r.fetchPartialState(fctx, srv, rem.roomID, joinEventID, version, join, joinRow.Sender)
		cancel()
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", srv, err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		stateIDs := make(map[string]bool, len(pj.state))
		for _, p := range pj.state {
			stateIDs[p.EventID()] = true
		}
		items := make([]joinRemainderItem, 0, len(pj.events))
		for id, p := range pj.events {
			if id != joinEventID {
				items = append(items, joinRemainderItem{p: p, state: stateIDs[id]})
			}
		}
		rem.items, rem.next, rem.written, rem.total, rem.refetch = items, 0, 0, len(items), false
		return nil
	}
	if lastErr == nil {
		lastErr = errors.New("no server to ask")
	}
	return lastErr
}

func removeString(list []string, drop string) []string {
	out := list[:0:0]
	for _, s := range list {
		if s != drop {
			out = append(out, s)
		}
	}
	return out
}

// backgroundStateBodyLimit caps the background /state fetch of a partial room.
// It equals the send_join cap unless MATRIX_JOIN_BG_FETCH_MAX_MB (1..500) is
// set: after an omit_members join the full member list is only ever fetched
// here, and a very large room may need more than the join response did.
func backgroundStateBodyLimit() int64 {
	if mb := envInt("MATRIX_JOIN_BG_FETCH_MAX_MB", 0, 0, 500); mb > 0 {
		return int64(mb) << 20
	}
	return sendJoinBodyLimit()
}

func (r *RoomService) fetchPartialState(ctx context.Context, server, roomID, joinEventID, version string, join gmsl.PDU, userID string) (*preparedFederationJoin, error) {
	client := newFederationClient(r.HS)
	resp, err := client.GetStateContext(ctx, server, roomID, joinEventID, backgroundStateBodyLimit())
	if err != nil {
		return nil, err
	}
	// The returned state contains our own join event; it is already stored and
	// verified, so it is not validated (or key-fetched) again.
	resp.State = withoutMember(resp.State, userID)
	return r.validateFederationStateSet(withKeyNotaries(ctx, server), roomID, userID, version, join, resp, true)
}

// withoutMember drops userID's own m.room.member event from raw state.
func withoutMember(raw []json.RawMessage, userID string) []json.RawMessage {
	out := raw[:0]
	for _, item := range raw {
		var h struct {
			Type     string  `json:"type"`
			StateKey *string `json:"state_key"`
		}
		if json.Unmarshal(item, &h) == nil && h.Type == "m.room.member" && h.StateKey != nil && *h.StateKey == userID {
			continue
		}
		out = append(out, item)
	}
	return out
}
