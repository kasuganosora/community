package homeserver

import (
	"fmt"
	"sort"
	"testing"
)

// ===== State Resolution v2 Tests =====
// Based on the scenarios described in gomatrixserverlib/stateresolutionv2_test.go
// and the Matrix spec (Room Version 2+).

// Helper: create a MatrixEvent with given properties.
func makeEvent(eventID, roomID string, eventType EventType, sender string, stateKey *string, content map[string]any, depth int64, ts int64, authEvents []string) *MatrixEvent {
	return &MatrixEvent{
		EventID:        eventID,
		RoomID:         roomID,
		EventType:      eventType,
		Sender:         sender,
		StateKey:       stateKey,
		Content:        content,
		Depth:          depth,
		OriginServerTS: ts,
		AuthEvents:     authEvents,
	}
}

// buildTestGraph creates a base event graph for testing, similar to
// gomatrixserverlib's getBaseStateResV2Graph().
//
// Graph:
//
//	$create (m.room.create, creator=Alice)
//	$alice_join (m.room.member, Alice joins)
//	$bob_join (m.room.member, Bob joins)
//	$power_levels (m.room.power_levels, Alice=100)
//	$join_rules (m.room.join_rules, "public")
func buildTestGraph() ([]*MatrixEvent, map[string]*MatrixEvent) {
	const roomID = "!testroom:example.com"

	alice := "@alice:example.com" //nolint:goconst
	bob := "@bob:example.com"     //nolint:goconst

	create := makeEvent("$create", roomID, EventTypeRoomCreate, alice, new(""),
		map[string]any{"creator": alice, "room_version": "10"},
		1, 1000, nil)

	aliceJoin := makeEvent("$alice_join", roomID, EventTypeRoomMember, alice, new(alice),
		map[string]any{"membership": "join"}, //nolint:goconst
		2, 2000, []string{"$create"})         //nolint:goconst

	powerLevels := makeEvent("$power_levels", roomID, EventTypeRoomPowerLevels, alice, new(""),
		map[string]any{
			"users":          map[string]any{alice: float64(100), bob: float64(0)}, //nolint:goconst
			"users_default":  float64(0),                                           //nolint:goconst
			"events_default": float64(0),                                           //nolint:goconst
			"state_default":  float64(50),                                          //nolint:goconst
			"ban":            float64(50),                                          //nolint:goconst
			"kick":           float64(50),                                          //nolint:goconst
			"redact":         float64(50),                                          //nolint:goconst
			"invite":         float64(0),                                           //nolint:goconst
		},
		3, 3000, []string{"$create", "$alice_join"}) //nolint:goconst

	joinRules := makeEvent("$join_rules", roomID, EventTypeRoomJoinRules, alice, new(""),
		map[string]any{"join_rule": "public"},                        //nolint:goconst
		4, 4000, []string{"$create", "$power_levels", "$alice_join"}) //nolint:goconst

	bobJoin := makeEvent("$bob_join", roomID, EventTypeRoomMember, bob, new(bob),
		map[string]any{"membership": "join"},
		5, 5000, []string{"$create", "$power_levels", "$join_rules"}) //nolint:goconst

	events := []*MatrixEvent{create, aliceJoin, powerLevels, joinRules, bobJoin}

	eventMap := make(map[string]*MatrixEvent)

	for _, e := range events {
		eventMap[e.EventID] = e
	}

	return events, eventMap
}

// Test: unconflicted state is preserved as-is.
func TestStateResV2_Unconflicted(t *testing.T) {
	events, _ := buildTestGraph()
	resolver := NewStateResolverWithEvents("!testroom:example.com", events)

	// Two identical state sets → no conflict
	stateSet := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: events[0]},                       //nolint:goconst
		{Tuple: StateKeyTuple{"m.room.member", "@alice:example.com"}, EventID: "$alice_join", Event: events[1]}, //nolint:goconst
	}

	result, err := resolver.ResolveState([][]StateEntry{stateSet, stateSet})
	if err != nil {
		t.Fatalf("ResolveState failed: %v", err)
	}

	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}

	// Verify all entries are present
	resultMap := make(map[string]string)
	for _, entry := range result {
		resultMap[entry.Tuple.EventType+"/"+entry.Tuple.StateKey] = entry.EventID
	}

	if resultMap["m.room.create/"] != "$create" {
		t.Errorf("expected create event $create, got %s", resultMap["m.room.create/"])
	}

	if resultMap["m.room.member/@alice:example.com"] != "$alice_join" {
		t.Errorf("expected alice join event $alice_join, got %s", resultMap["m.room.member/@alice:example.com"])
	}
}

// Test: single state set returns itself.
func TestStateResV2_SingleSet(t *testing.T) {
	events, _ := buildTestGraph()
	resolver := NewStateResolverWithEvents("!testroom:example.com", events)

	stateSet := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: events[0]},
	}

	result, err := resolver.ResolveState([][]StateEntry{stateSet})
	if err != nil {
		t.Fatalf("ResolveState failed: %v", err)
	}

	if len(result) != 1 || result[0].EventID != "$create" {
		t.Fatalf("expected single $create entry, got %v", result)
	}
}

// Test: empty state returns nil.
func TestStateResV2_Empty(t *testing.T) {
	resolver := NewStateResolverWithEvents("!testroom:example.com", nil)

	result, err := resolver.ResolveState(nil)
	if err != nil {
		t.Fatalf("ResolveState failed: %v", err)
	}

	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}
}

// Test: control event conflict — competing power level changes.
// Alice (PL=100) changes PL; a forked branch has Bob (PL=0) trying to change PL.
// Alice's event should win because she has higher power level.
func TestStateResV2_ControlEventConflict_PowerLevel(t *testing.T) {
	const roomID = "!testroom:example.com"

	alice := "@alice:example.com"
	bob := "@bob:example.com"

	events, eventMap := buildTestGraph()

	// Fork: Alice sets new power levels (Bob gets PL=50)
	alicePL := makeEvent("$alice_pl", roomID, EventTypeRoomPowerLevels, alice, new(""),
		map[string]any{
			"users":          map[string]any{alice: float64(100), bob: float64(50)},
			"users_default":  float64(0),
			"events_default": float64(0),
			"state_default":  float64(50),
			"ban":            float64(50),
			"kick":           float64(50),
			"redact":         float64(50),
			"invite":         float64(0),
		},
		6, 6000, []string{"$create", "$power_levels", "$alice_join"})

	// Fork: Bob tries to set power levels (which shouldn't pass v1, but let's see v2 handle it)
	bobPL := makeEvent("$bob_pl", roomID, EventTypeRoomPowerLevels, bob, new(""),
		map[string]any{
			"users":          map[string]any{alice: float64(0), bob: float64(100)},
			"users_default":  float64(0),
			"events_default": float64(0),
			"state_default":  float64(50),
			"ban":            float64(50),
			"kick":           float64(50),
			"redact":         float64(50),
			"invite":         float64(0),
		},
		6, 6500, []string{"$create", "$power_levels", "$bob_join"}) //nolint:goconst

	allEvents := append(append([]*MatrixEvent{}, events...), alicePL, bobPL)
	for _, e := range allEvents {
		eventMap[e.EventID] = e
	}

	resolver := NewStateResolverWithEvents(roomID, allEvents)

	// State set 1: sees Alice's PL change
	set1 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: eventMap["$create"]},
		{Tuple: StateKeyTuple{"m.room.member", alice}, EventID: "$alice_join", Event: eventMap["$alice_join"]},
		{Tuple: StateKeyTuple{"m.room.member", bob}, EventID: "$bob_join", Event: eventMap["$bob_join"]},
		{Tuple: StateKeyTuple{"m.room.power_levels", ""}, EventID: "$alice_pl", Event: alicePL},                 //nolint:goconst
		{Tuple: StateKeyTuple{"m.room.join_rules", ""}, EventID: "$join_rules", Event: eventMap["$join_rules"]}, //nolint:goconst
	}

	// State set 2: sees Bob's PL change
	set2 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: eventMap["$create"]},
		{Tuple: StateKeyTuple{"m.room.member", alice}, EventID: "$alice_join", Event: eventMap["$alice_join"]},
		{Tuple: StateKeyTuple{"m.room.member", bob}, EventID: "$bob_join", Event: eventMap["$bob_join"]},
		{Tuple: StateKeyTuple{"m.room.power_levels", ""}, EventID: "$bob_pl", Event: bobPL},
		{Tuple: StateKeyTuple{"m.room.join_rules", ""}, EventID: "$join_rules", Event: eventMap["$join_rules"]},
	}

	result, err := resolver.ResolveState([][]StateEntry{set1, set2})
	if err != nil {
		t.Fatalf("ResolveState failed: %v", err)
	}

	// Find the resolved power levels event
	var resolvedPL string

	for _, entry := range result {
		if entry.Tuple.EventType == "m.room.power_levels" {
			resolvedPL = entry.EventID

			break
		}
	}

	// Alice's PL change should win (she has PL=100, Bob has PL=0)
	if resolvedPL != "$alice_pl" {
		t.Errorf("expected Alice's power level event ($alice_pl) to win, got %s", resolvedPL)
	}
}

// Test: non-control event conflict — two competing room name changes.
// The one from the user with higher power level should win.
func TestStateResV2_NonControlConflict_RoomName(t *testing.T) {
	const roomID = "!testroom:example.com"

	alice := "@alice:example.com"
	bob := "@bob:example.com"

	events, eventMap := buildTestGraph()

	// Give Bob state_default access (PL=50 grants state setting)
	powerLevels := makeEvent("$power_levels_v2", roomID, EventTypeRoomPowerLevels, alice, new(""),
		map[string]any{
			"users":          map[string]any{alice: float64(100), bob: float64(50)},
			"users_default":  float64(0),
			"events_default": float64(0),
			"state_default":  float64(50),
			"ban":            float64(50),
			"kick":           float64(50),
			"redact":         float64(50),
			"invite":         float64(0),
		},
		6, 6000, []string{"$create", "$power_levels", "$alice_join"})
	eventMap["$power_levels_v2"] = powerLevels

	// Fork: Alice sets room name
	aliceName := makeEvent("$alice_name", roomID, EventTypeRoomName, alice, new(""),
		map[string]any{"name": "Alice's Room"},                          //nolint:goconst
		7, 7000, []string{"$create", "$power_levels_v2", "$alice_join"}) //nolint:goconst
	eventMap["$alice_name"] = aliceName

	// Fork: Bob sets room name
	bobName := makeEvent("$bob_name", roomID, EventTypeRoomName, bob, new(""),
		map[string]any{"name": "Bob's Room"},
		7, 7500, []string{"$create", "$power_levels_v2", "$bob_join"})
	eventMap["$bob_name"] = bobName

	allEvents := append(append([]*MatrixEvent{}, events...), powerLevels, aliceName, bobName)
	resolver := NewStateResolverWithEvents(roomID, allEvents)

	set1 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: eventMap["$create"]},
		{Tuple: StateKeyTuple{"m.room.member", alice}, EventID: "$alice_join", Event: eventMap["$alice_join"]},
		{Tuple: StateKeyTuple{"m.room.member", bob}, EventID: "$bob_join", Event: eventMap["$bob_join"]},
		{Tuple: StateKeyTuple{"m.room.power_levels", ""}, EventID: "$power_levels_v2", Event: powerLevels},
		{Tuple: StateKeyTuple{"m.room.join_rules", ""}, EventID: "$join_rules", Event: eventMap["$join_rules"]},
		{Tuple: StateKeyTuple{"m.room.name", ""}, EventID: "$alice_name", Event: aliceName}, //nolint:goconst
	}

	set2 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: eventMap["$create"]},
		{Tuple: StateKeyTuple{"m.room.member", alice}, EventID: "$alice_join", Event: eventMap["$alice_join"]},
		{Tuple: StateKeyTuple{"m.room.member", bob}, EventID: "$bob_join", Event: eventMap["$bob_join"]},
		{Tuple: StateKeyTuple{"m.room.power_levels", ""}, EventID: "$power_levels_v2", Event: powerLevels},
		{Tuple: StateKeyTuple{"m.room.join_rules", ""}, EventID: "$join_rules", Event: eventMap["$join_rules"]},
		{Tuple: StateKeyTuple{"m.room.name", ""}, EventID: "$bob_name", Event: bobName}, //nolint:goconst
	}

	result, err := resolver.ResolveState([][]StateEntry{set1, set2})
	if err != nil {
		t.Fatalf("ResolveState failed: %v", err)
	}

	// Both events should pass auth (both have sufficient PL).
	// In v2, events are sorted by mainline position, then origin_server_ts ascending.
	// Events are applied iteratively — the LAST event to pass auth wins.
	// Bob's event has later timestamp (7500 > 7000), so it is processed last and wins.
	var resolvedName string

	for _, entry := range result {
		if entry.Tuple.EventType == "m.room.name" {
			resolvedName = entry.EventID

			break
		}
	}

	if resolvedName != "$bob_name" {
		t.Errorf("expected Bob's room name ($bob_name) to win (later timestamp, applied last), got %s", resolvedName)
	}
}

// Test: isControlEvent classification.
func TestIsControlEvent(t *testing.T) {
	alice := "@alice:example.com"
	bob := "@bob:example.com"

	tests := []struct {
		name     string
		event    *MatrixEvent
		expected bool
	}{
		{
			name: "power levels is control",
			event: &MatrixEvent{
				EventType: EventTypeRoomPowerLevels,
				StateKey:  new(""),
				Sender:    alice,
			},
			expected: true,
		},
		{
			name: "join rules is control",
			event: &MatrixEvent{
				EventType: EventTypeRoomJoinRules,
				StateKey:  new(""),
				Sender:    alice,
			},
			expected: true,
		},
		{
			name: "ban is control (membership=ban, sender!=state_key)",
			event: &MatrixEvent{
				EventType: EventTypeRoomMember,
				StateKey:  new(bob),
				Sender:    alice,
				Content:   map[string]any{"membership": "ban"},
			},
			expected: true,
		},
		{
			name: "kick is control (membership=leave, sender!=state_key)",
			event: &MatrixEvent{
				EventType: EventTypeRoomMember,
				StateKey:  new(bob),
				Sender:    alice,
				Content:   map[string]any{"membership": "leave"}, //nolint:goconst
			},
			expected: true,
		},
		{
			name: "self-leave is NOT control",
			event: &MatrixEvent{
				EventType: EventTypeRoomMember,
				StateKey:  new(alice),
				Sender:    alice,
				Content:   map[string]any{"membership": "leave"},
			},
			expected: false,
		},
		{
			name: "join is NOT control",
			event: &MatrixEvent{
				EventType: EventTypeRoomMember,
				StateKey:  new(alice),
				Sender:    alice,
				Content:   map[string]any{"membership": "join"},
			},
			expected: false,
		},
		{
			name: "room name is NOT control",
			event: &MatrixEvent{
				EventType: EventTypeRoomName,
				StateKey:  new(""),
				Sender:    alice,
			},
			expected: false,
		},
		{
			name: "room topic is NOT control",
			event: &MatrixEvent{
				EventType: EventTypeRoomTopic,
				StateKey:  new(""),
				Sender:    alice,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isControlEvent(tt.event)
			if result != tt.expected {
				t.Errorf("isControlEvent() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// Test: auth chain difference calculation.
func TestStateResV2_AuthChainDifference(t *testing.T) {
	const roomID = "!testroom:example.com"

	alice := "@alice:example.com"
	bob := "@bob:example.com"

	create := makeEvent("$create", roomID, EventTypeRoomCreate, alice, new(""),
		map[string]any{"creator": alice}, 1, 1000, nil)

	aliceJoin := makeEvent("$alice_join", roomID, EventTypeRoomMember, alice, new(alice),
		map[string]any{"membership": "join"}, 2, 2000, []string{"$create"})

	bobJoin := makeEvent("$bob_join", roomID, EventTypeRoomMember, bob, new(bob),
		map[string]any{"membership": "join"}, 3, 3000, []string{"$create"})

	// Event A references alice_join in auth chain
	eventA := makeEvent("$eventA", roomID, EventTypeRoomName, alice, new(""),
		map[string]any{"name": "A"}, 4, 4000, []string{"$create", "$alice_join"})

	// Event B references bob_join in auth chain
	eventB := makeEvent("$eventB", roomID, EventTypeRoomName, bob, new(""),
		map[string]any{"name": "B"}, 4, 4500, []string{"$create", "$bob_join"})

	allEvents := []*MatrixEvent{create, aliceJoin, bobJoin, eventA, eventB}
	resolver := NewStateResolverWithEvents(roomID, allEvents)

	conflicted := map[string]*MatrixEvent{
		"$eventA": eventA,
		"$eventB": eventB,
	}

	diff, err := resolver.computeAuthDifference(conflicted)
	if err != nil {
		t.Fatalf("computeAuthDifference failed: %v", err)
	}

	// Auth chain of A: {$create, $alice_join}
	// Auth chain of B: {$create, $bob_join}
	// Intersection: {$create}
	// Difference: {$alice_join, $bob_join}
	diffIDs := make(map[string]bool)
	for _, e := range diff {
		diffIDs[e.EventID] = true
	}

	if !diffIDs["$alice_join"] {
		t.Error("expected $alice_join in auth chain difference")
	}

	if !diffIDs["$bob_join"] {
		t.Error("expected $bob_join in auth chain difference")
	}

	if diffIDs["$create"] {
		t.Error("$create should NOT be in auth chain difference (it's in the intersection)")
	}
}

// Test: reverse topological ordering preserves auth dependencies.
func TestStateResV2_ReverseTopologicalOrdering(t *testing.T) {
	const roomID = "!testroom:example.com"

	alice := "@alice:example.com"

	create := makeEvent("$create", roomID, EventTypeRoomCreate, alice, new(""),
		map[string]any{"creator": alice}, 1, 1000, nil)

	pl1 := makeEvent("$pl1", roomID, EventTypeRoomPowerLevels, alice, new(""),
		map[string]any{
			"users":         map[string]any{alice: float64(100)},
			"users_default": float64(0),
		},
		2, 2000, []string{"$create"})

	pl2 := makeEvent("$pl2", roomID, EventTypeRoomPowerLevels, alice, new(""),
		map[string]any{
			"users":         map[string]any{alice: float64(100)},
			"users_default": float64(0),
		},
		3, 3000, []string{"$create", "$pl1"})

	allEvents := []*MatrixEvent{create, pl1, pl2}
	resolver := NewStateResolverWithEvents(roomID, allEvents)

	// Topological sort should ensure $create comes before $pl1, and $pl1 before $pl2
	sorted := resolver.reverseTopologicalOrdering([]*MatrixEvent{pl2, create, pl1}, true)

	posOf := make(map[string]int)
	for i, e := range sorted {
		posOf[e.EventID] = i
	}

	if posOf["$create"] >= posOf["$pl1"] {
		t.Errorf("$create (pos=%d) should come before $pl1 (pos=%d)", posOf["$create"], posOf["$pl1"])
	}

	if posOf["$pl1"] >= posOf["$pl2"] {
		t.Errorf("$pl1 (pos=%d) should come before $pl2 (pos=%d)", posOf["$pl1"], posOf["$pl2"])
	}
}

// Test: ban event (control event) overrides join from forked branch.
func TestStateResV2_BanOverridesJoin(t *testing.T) {
	const roomID = "!testroom:example.com"

	alice := "@alice:example.com"
	bob := "@bob:example.com"
	charlie := "@charlie:example.com"

	events, eventMap := buildTestGraph()

	// Charlie joins
	charlieJoin := makeEvent("$charlie_join", roomID, EventTypeRoomMember, charlie, new(charlie),
		map[string]any{"membership": "join"},
		6, 6000, []string{"$create", "$power_levels", "$join_rules"})
	eventMap["$charlie_join"] = charlieJoin

	// Fork 1: Alice bans Charlie
	charlieBan := makeEvent("$charlie_ban", roomID, EventTypeRoomMember, alice, new(charlie),
		map[string]any{"membership": "ban"},
		7, 7000, []string{"$create", "$power_levels", "$alice_join", "$charlie_join"})
	eventMap["$charlie_ban"] = charlieBan

	// Fork 2: Charlie is still joined (different view of the DAG)
	charlieStillJoined := makeEvent("$charlie_join2", roomID, EventTypeRoomMember, charlie, new(charlie),
		map[string]any{"membership": "join"},
		7, 7500, []string{"$create", "$power_levels", "$join_rules", "$charlie_join"})
	eventMap["$charlie_join2"] = charlieStillJoined

	allEvents := append(append([]*MatrixEvent{}, events...), charlieJoin, charlieBan, charlieStillJoined)
	_ = bob // bob is a participant but not directly involved in this conflict

	resolver := NewStateResolverWithEvents(roomID, allEvents)

	// Set 1: Charlie is banned
	set1 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: eventMap["$create"]},
		{Tuple: StateKeyTuple{"m.room.member", alice}, EventID: "$alice_join", Event: eventMap["$alice_join"]},
		{Tuple: StateKeyTuple{"m.room.member", bob}, EventID: "$bob_join", Event: eventMap["$bob_join"]},
		{Tuple: StateKeyTuple{"m.room.power_levels", ""}, EventID: "$power_levels", Event: eventMap["$power_levels"]},
		{Tuple: StateKeyTuple{"m.room.join_rules", ""}, EventID: "$join_rules", Event: eventMap["$join_rules"]},
		{Tuple: StateKeyTuple{"m.room.member", charlie}, EventID: "$charlie_ban", Event: charlieBan},
	}

	// Set 2: Charlie is joined
	set2 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: eventMap["$create"]},
		{Tuple: StateKeyTuple{"m.room.member", alice}, EventID: "$alice_join", Event: eventMap["$alice_join"]},
		{Tuple: StateKeyTuple{"m.room.member", bob}, EventID: "$bob_join", Event: eventMap["$bob_join"]},
		{Tuple: StateKeyTuple{"m.room.power_levels", ""}, EventID: "$power_levels", Event: eventMap["$power_levels"]},
		{Tuple: StateKeyTuple{"m.room.join_rules", ""}, EventID: "$join_rules", Event: eventMap["$join_rules"]},
		{Tuple: StateKeyTuple{"m.room.member", charlie}, EventID: "$charlie_join2", Event: charlieStillJoined},
	}

	result, err := resolver.ResolveState([][]StateEntry{set1, set2})
	if err != nil {
		t.Fatalf("ResolveState failed: %v", err)
	}

	// The ban event from Alice (PL=100) is a control event and should be processed first.
	// Charlie's re-join should fail auth check (banned users can't join).
	var resolvedCharlie string

	for _, entry := range result {
		if entry.Tuple == (StateKeyTuple{"m.room.member", charlie}) {
			resolvedCharlie = entry.EventID

			break
		}
	}

	if resolvedCharlie != "$charlie_ban" {
		t.Errorf("expected ban to win over join, got %s", resolvedCharlie)
	}
}

// Test: separateConflicted correctly identifies conflicted vs unconflicted entries.
func TestStateResV2_SeparateConflicted(t *testing.T) {
	events, _ := buildTestGraph()
	resolver := NewStateResolverWithEvents("!testroom:example.com", events)

	alice := "@alice:example.com"
	bob := "@bob:example.com"

	// Identical entries in both sets
	set1 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: events[0]},
		{Tuple: StateKeyTuple{"m.room.member", alice}, EventID: "$alice_join", Event: events[1]},
		{Tuple: StateKeyTuple{"m.room.name", ""}, EventID: "$name_a", Event: makeEvent("$name_a", "!testroom:example.com", EventTypeRoomName, alice, new(""), map[string]any{"name": "A"}, 6, 6000, nil)},
	}

	set2 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: events[0]},
		{Tuple: StateKeyTuple{"m.room.member", alice}, EventID: "$alice_join", Event: events[1]},
		{Tuple: StateKeyTuple{"m.room.name", ""}, EventID: "$name_b", Event: makeEvent("$name_b", "!testroom:example.com", EventTypeRoomName, bob, new(""), map[string]any{"name": "B"}, 6, 6500, nil)},
	}

	unconflicted, conflicted, err := resolver.separateConflicted([][]StateEntry{set1, set2})
	if err != nil {
		t.Fatalf("separateConflicted failed: %v", err)
	}

	// create and alice_join are identical → unconflicted
	if len(unconflicted) != 2 {
		t.Errorf("expected 2 unconflicted entries, got %d", len(unconflicted))
	}

	// room name differs → conflicted
	if len(conflicted) != 1 {
		t.Errorf("expected 1 conflicted tuple, got %d", len(conflicted))
	}

	nameKey := StateKeyTuple{"m.room.name", ""}
	if entries, ok := conflicted[nameKey]; !ok {
		t.Error("expected m.room.name to be conflicted")
	} else if len(entries) != 2 {
		t.Errorf("expected 2 conflicting entries for room name, got %d", len(entries))
	}
}

// Test: events missing from one state set are conflicted (v2 stricter definition).
func TestStateResV2_MissingEventIsConflicted(t *testing.T) {
	events, _ := buildTestGraph()
	resolver := NewStateResolverWithEvents("!testroom:example.com", events)

	// Set 1 has room name, Set 2 doesn't
	nameEvent := makeEvent("$name", "!testroom:example.com", EventTypeRoomName, "@alice:example.com", new(""),
		map[string]any{"name": "test"}, 6, 6000, nil)

	set1 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: events[0]},
		{Tuple: StateKeyTuple{"m.room.name", ""}, EventID: "$name", Event: nameEvent},
	}

	set2 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: events[0]},
	}

	unconflicted, conflicted, err := resolver.separateConflicted([][]StateEntry{set1, set2})
	if err != nil {
		t.Fatalf("separateConflicted failed: %v", err)
	}

	// create is in both → unconflicted
	if len(unconflicted) != 1 {
		t.Errorf("expected 1 unconflicted entry, got %d", len(unconflicted))
	}

	// room name only in set1 → conflicted per v2 rules
	if len(conflicted) != 1 {
		t.Errorf("expected 1 conflicted tuple (room name), got %d", len(conflicted))
	}
}

// Test: ComputeStateDiff detects additions, removals, and modifications.
func TestComputeStateDiff(t *testing.T) {
	oldState := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.name", ""}, EventID: "$name1"},
		{Tuple: StateKeyTuple{"m.room.topic", ""}, EventID: "$topic1"},
	}

	newState := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.name", ""}, EventID: "$name2"}, // modified
		{Tuple: StateKeyTuple{"m.room.avatar", ""}, EventID: "$av1"}, // added
		// topic1 removed
	}

	added, removed, modified := ComputeStateDiff(oldState, newState)

	if len(added) != 1 || added[0].EventID != "$av1" {
		t.Errorf("expected 1 added ($av1), got %v", added)
	}

	if len(removed) != 1 || removed[0].EventID != "$topic1" {
		t.Errorf("expected 1 removed ($topic1), got %v", removed)
	}

	if len(modified) != 1 || modified[0].EventID != "$name2" {
		t.Errorf("expected 1 modified ($name2), got %v", modified)
	}
}

// Test: power level sorting — higher PL wins, then earlier timestamp, then event ID.
func TestStateResV2_ControlEventSorting(t *testing.T) {
	alice := "@alice:example.com"
	bob := "@bob:example.com"

	plEvent := makeEvent("$pl", "!room", EventTypeRoomPowerLevels, alice, new(""),
		map[string]any{
			"users":         map[string]any{alice: float64(100), bob: float64(50)},
			"users_default": float64(0),
		}, 1, 1000, nil)

	// Alice PL=100, timestamp=2000
	eventA := makeEvent("$a", "!room", EventTypeRoomPowerLevels, alice, new(""),
		map[string]any{}, 2, 2000, []string{"$pl"}) //nolint:goconst

	// Bob PL=50, timestamp=1500 (earlier but lower PL)
	eventB := makeEvent("$b", "!room", EventTypeRoomPowerLevels, bob, new(""),
		map[string]any{}, 2, 1500, []string{"$pl"})

	allEvents := []*MatrixEvent{plEvent, eventA, eventB}
	resolver := NewStateResolverWithEvents("!room", allEvents)

	eventMap := map[string]*MatrixEvent{"$pl": plEvent, "$a": eventA, "$b": eventB}
	events := []*MatrixEvent{eventB, eventA} // intentionally reversed

	resolver.sortControlEvents(events, eventMap)

	// Alice (PL=100) should come first regardless of timestamp
	if events[0].EventID != "$a" {
		t.Errorf("expected Alice's event first (higher PL), got %s", events[0].EventID)
	}

	if events[1].EventID != "$b" {
		t.Errorf("expected Bob's event second, got %s", events[1].EventID)
	}
}

// Test: lexicographic sorting tiebreaker.
func TestStateResV2_LexicographicSorting(t *testing.T) {
	const roomID = "!room:example.com"

	alice := "@alice:example.com"

	// Same sender (same PL), same timestamp → sort by event ID
	eventA := makeEvent("$aaa", roomID, EventTypeRoomPowerLevels, alice, new(""),
		map[string]any{}, 1, 1000, nil)
	eventB := makeEvent("$zzz", roomID, EventTypeRoomPowerLevels, alice, new(""),
		map[string]any{}, 1, 1000, nil)

	allEvents := []*MatrixEvent{eventA, eventB}
	resolver := NewStateResolverWithEvents(roomID, allEvents)

	eventMap := map[string]*MatrixEvent{"$aaa": eventA, "$zzz": eventB}
	events := []*MatrixEvent{eventB, eventA}

	resolver.sortControlEvents(events, eventMap)

	if events[0].EventID != "$aaa" {
		t.Errorf("expected $aaa first (lexicographic), got %s", events[0].EventID)
	}
}

// Test: full integration — resolves multiple conflicts simultaneously.
func TestStateResV2_MultipleConflicts(t *testing.T) {
	const roomID = "!testroom:example.com"

	alice := "@alice:example.com"
	bob := "@bob:example.com"

	events, eventMap := buildTestGraph()

	// Fork: different room names from different users
	aliceName := makeEvent("$alice_name", roomID, EventTypeRoomName, alice, new(""),
		map[string]any{"name": "Alice's Room"},
		6, 6000, []string{"$create", "$power_levels", "$alice_join"})
	eventMap["$alice_name"] = aliceName

	bobName := makeEvent("$bob_name", roomID, EventTypeRoomName, bob, new(""),
		map[string]any{"name": "Bob's Room"},
		6, 6500, []string{"$create", "$power_levels", "$bob_join"})
	eventMap["$bob_name"] = bobName

	// Fork: different topics
	aliceTopic := makeEvent("$alice_topic", roomID, EventTypeRoomTopic, alice, new(""),
		map[string]any{"topic": "Alice's topic"},
		6, 6100, []string{"$create", "$power_levels", "$alice_join"})
	eventMap["$alice_topic"] = aliceTopic

	bobTopic := makeEvent("$bob_topic", roomID, EventTypeRoomTopic, bob, new(""),
		map[string]any{"topic": "Bob's topic"},
		6, 6200, []string{"$create", "$power_levels", "$bob_join"})
	eventMap["$bob_topic"] = bobTopic

	allEvents := append(append([]*MatrixEvent{}, events...), aliceName, bobName, aliceTopic, bobTopic)
	resolver := NewStateResolverWithEvents(roomID, allEvents)

	set1 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: eventMap["$create"]},
		{Tuple: StateKeyTuple{"m.room.member", alice}, EventID: "$alice_join", Event: eventMap["$alice_join"]},
		{Tuple: StateKeyTuple{"m.room.member", bob}, EventID: "$bob_join", Event: eventMap["$bob_join"]},
		{Tuple: StateKeyTuple{"m.room.power_levels", ""}, EventID: "$power_levels", Event: eventMap["$power_levels"]},
		{Tuple: StateKeyTuple{"m.room.join_rules", ""}, EventID: "$join_rules", Event: eventMap["$join_rules"]},
		{Tuple: StateKeyTuple{"m.room.name", ""}, EventID: "$alice_name", Event: aliceName},
		{Tuple: StateKeyTuple{"m.room.topic", ""}, EventID: "$alice_topic", Event: aliceTopic},
	}

	set2 := []StateEntry{
		{Tuple: StateKeyTuple{"m.room.create", ""}, EventID: "$create", Event: eventMap["$create"]},
		{Tuple: StateKeyTuple{"m.room.member", alice}, EventID: "$alice_join", Event: eventMap["$alice_join"]},
		{Tuple: StateKeyTuple{"m.room.member", bob}, EventID: "$bob_join", Event: eventMap["$bob_join"]},
		{Tuple: StateKeyTuple{"m.room.power_levels", ""}, EventID: "$power_levels", Event: eventMap["$power_levels"]},
		{Tuple: StateKeyTuple{"m.room.join_rules", ""}, EventID: "$join_rules", Event: eventMap["$join_rules"]},
		{Tuple: StateKeyTuple{"m.room.name", ""}, EventID: "$bob_name", Event: bobName},
		{Tuple: StateKeyTuple{"m.room.topic", ""}, EventID: "$bob_topic", Event: bobTopic},
	}

	result, err := resolver.ResolveState([][]StateEntry{set1, set2})
	if err != nil {
		t.Fatalf("ResolveState failed: %v", err)
	}

	resultMap := make(map[string]string)

	for _, entry := range result {
		key := fmt.Sprintf("%s/%s", entry.Tuple.EventType, entry.Tuple.StateKey)
		resultMap[key] = entry.EventID
	}

	// Unconflicted entries should be preserved
	if resultMap["m.room.create/"] != "$create" {
		t.Errorf("create should be $create, got %s", resultMap["m.room.create/"])
	}

	if resultMap["m.room.power_levels/"] != "$power_levels" {
		t.Errorf("power_levels should be $power_levels, got %s", resultMap["m.room.power_levels/"])
	}

	// Check that name and topic were resolved (exact winner depends on auth check + ordering)
	if _, ok := resultMap["m.room.name/"]; !ok {
		t.Error("expected room name to be in result")
	}

	if _, ok := resultMap["m.room.topic/"]; !ok {
		t.Error("expected room topic to be in result")
	}

	// Bob has PL=0 (from original $power_levels), state_default=50.
	// Bob's events fail auth (0 < 50), so Alice's events win.
	if resultMap["m.room.name/"] != "$alice_name" {
		t.Errorf("expected $alice_name (Bob fails auth), got %s", resultMap["m.room.name/"])
	}

	if resultMap["m.room.topic/"] != "$alice_topic" {
		t.Errorf("expected $alice_topic (Bob fails auth), got %s", resultMap["m.room.topic/"])
	}

	// All unconflicted entries should be present
	expectedCount := 7 // create, alice, bob, PL, join_rules, name, topic
	if len(result) != expectedCount {
		// Print what we got
		ids := make([]string, 0, len(result))
		for _, e := range result {
			ids = append(ids, fmt.Sprintf("%s/%s=%s", e.Tuple.EventType, e.Tuple.StateKey, e.EventID))
		}

		sort.Strings(ids)
		t.Errorf("expected %d entries, got %d: %v", expectedCount, len(result), ids)
	}
}
