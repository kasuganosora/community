package homeserver

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
)

// ===== 状态解析算法 v2（基于 Matrix Spec Room Version 2+）=====
//
// 参考: https://spec.matrix.org/v1.13/rooms/v2/
// 参考: gomatrixserverlib/stateresolutionv2.go

// StateResolver 状态解析器
type StateResolver struct {
	db       *gorm.DB
	roomID   string
	roomInfo *MatrixRoom
	// eventCache 事件缓存，避免重复加载
	eventCache map[string]*MatrixEvent
}

// NewStateResolver 创建状态解析器
func NewStateResolver(roomID string) *StateResolver {
	return &StateResolver{
		db:         db.GetDB(),
		roomID:     roomID,
		eventCache: make(map[string]*MatrixEvent),
	}
}

// NewStateResolverWithEvents 创建携带预加载事件的状态解析器（用于测试）
func NewStateResolverWithEvents(roomID string, events []*MatrixEvent) *StateResolver {
	cache := make(map[string]*MatrixEvent, len(events))
	for _, e := range events {
		cache[e.EventID] = e
	}
	return &StateResolver{
		roomID:     roomID,
		eventCache: cache,
	}
}

// StateKeyTuple 状态键元组（事件类型 + 状态键）
type StateKeyTuple struct {
	EventType string
	StateKey  string
}

// StateEntry 状态条目
type StateEntry struct {
	Tuple   StateKeyTuple
	EventID string
	Event   *MatrixEvent
}

// ===== State Resolution v2 Algorithm =====

// ResolveState 使用 v2 算法解析状态冲突。
// 输入：多个状态集合（代表 DAG 中不同分支的房间状态）
// 输出：解析后的统一状态
func (r *StateResolver) ResolveState(stateSets [][]StateEntry) ([]StateEntry, error) {
	if len(stateSets) == 0 {
		return nil, nil
	}
	if len(stateSets) == 1 {
		return stateSets[0], nil
	}

	// Phase 0: 分类 unconflicted 和 conflicted
	unconflicted, conflicted, err := r.separateConflicted(stateSets)
	if err != nil {
		return nil, err
	}

	if len(conflicted) == 0 {
		return unconflicted, nil
	}

	// 收集所有 conflicted 事件
	allConflicted := make(map[string]*MatrixEvent)
	for _, entries := range conflicted {
		for _, e := range entries {
			if e.Event != nil {
				allConflicted[e.EventID] = e.Event
			}
		}
	}

	// Phase 1: 计算 auth chain difference 并加入 conflicted 集合
	authDiffEvents, err := r.computeAuthDifference(allConflicted)
	if err != nil {
		return nil, err
	}
	for _, e := range authDiffEvents {
		if e.IsStateEvent() {
			allConflicted[e.EventID] = e
		}
	}

	// Phase 2: 分离 control events 和 other events
	controlEvents, otherEvents := r.classifyEvents(allConflicted)

	// Phase 3: 对 control events 进行反向拓扑排序（Kahn's algorithm on auth DAG），
	// 然后按 (sender power level desc, origin_server_ts asc, event_id asc) 排序
	sortedControl := r.reverseTopologicalOrdering(controlEvents, true)

	// Phase 4: 从 unconflicted state 开始，逐一 auth-check control events
	partialState := r.buildStateMap(unconflicted)
	r.authAndApplyEvents(sortedControl, partialState, allConflicted)

	// Phase 5: 创建 power level mainline
	mainline := r.createPowerLevelMainline(partialState, allConflicted)

	// Phase 6: 对 other events 进行 mainline ordering
	sortedOther := r.mainlineOrdering(otherEvents, mainline, allConflicted)

	// Phase 7: 从当前 partial state 开始，逐一 auth-check other events
	r.authAndApplyEvents(sortedOther, partialState, allConflicted)

	// Phase 8: 将 unconflicted state 重新覆盖到 partial state（unconflicted 始终优先）
	for _, entry := range unconflicted {
		key := StateKeyTuple{entry.Tuple.EventType, entry.Tuple.StateKey}
		partialState[key] = entry
	}

	// 转换回列表
	result := make([]StateEntry, 0, len(partialState))
	for _, entry := range partialState {
		result = append(result, entry)
	}

	return result, nil
}

// separateConflicted 将多个状态集合分为 unconflicted（所有集合一致）和 conflicted（存在差异）
func (r *StateResolver) separateConflicted(stateSets [][]StateEntry) (unconflicted []StateEntry, conflicted map[StateKeyTuple][]StateEntry, err error) {
	// 收集所有 tuple → 按集合索引的 event ID
	tupleToEvents := make(map[StateKeyTuple]map[string]*MatrixEvent)
	tuplePresence := make(map[StateKeyTuple]int) // 在多少个集合中出现

	for _, stateSet := range stateSets {
		seen := make(map[StateKeyTuple]bool)
		for _, entry := range stateSet {
			if entry.Event == nil && entry.EventID != "" {
				entry.Event, _ = r.loadEvent(entry.EventID)
			}
			if tupleToEvents[entry.Tuple] == nil {
				tupleToEvents[entry.Tuple] = make(map[string]*MatrixEvent)
			}
			if entry.Event != nil {
				tupleToEvents[entry.Tuple][entry.EventID] = entry.Event
			}
			if !seen[entry.Tuple] {
				seen[entry.Tuple] = true
				tuplePresence[entry.Tuple]++
			}
		}
	}

	unconflicted = make([]StateEntry, 0)
	conflicted = make(map[StateKeyTuple][]StateEntry)
	numSets := len(stateSets)

	for tuple, events := range tupleToEvents {
		// v2 规定：事件必须在所有集合中出现且值相同才算 unconflicted
		if len(events) == 1 && tuplePresence[tuple] == numSets {
			for eid, evt := range events {
				unconflicted = append(unconflicted, StateEntry{
					Tuple:   tuple,
					EventID: eid,
					Event:   evt,
				})
			}
		} else {
			entries := make([]StateEntry, 0, len(events))
			for eid, evt := range events {
				entries = append(entries, StateEntry{
					Tuple:   tuple,
					EventID: eid,
					Event:   evt,
				})
			}
			conflicted[tuple] = entries
		}
	}

	return unconflicted, conflicted, nil
}

// computeAuthDifference 计算 auth chain difference：出现在部分但不是所有 auth chain 中的事件
func (r *StateResolver) computeAuthDifference(conflicted map[string]*MatrixEvent) ([]*MatrixEvent, error) {
	// 为每个 conflicted event 构建 auth chain
	authChains := make([]map[string]*MatrixEvent, 0)
	allAuthEvents := make(map[string]*MatrixEvent)

	for _, event := range conflicted {
		chain := make(map[string]*MatrixEvent)
		r.buildAuthChainMap(event, chain)
		authChains = append(authChains, chain)
		for id, e := range chain {
			allAuthEvents[id] = e
		}
	}

	if len(authChains) == 0 {
		return nil, nil
	}

	// Auth chain difference = events NOT in the intersection of all chains
	var diffEvents []*MatrixEvent
	for id, event := range allAuthEvents {
		inAll := true
		for _, chain := range authChains {
			if _, ok := chain[id]; !ok {
				inAll = false
				break
			}
		}
		if !inAll {
			diffEvents = append(diffEvents, event)
		}
	}

	return diffEvents, nil
}

// buildAuthChainMap 递归构建 auth chain 到 map（用于集合运算）
func (r *StateResolver) buildAuthChainMap(event *MatrixEvent, chain map[string]*MatrixEvent) {
	if event == nil {
		return
	}
	for _, authID := range event.AuthEvents {
		if _, ok := chain[authID]; ok {
			continue
		}
		authEvent, err := r.loadEvent(authID)
		if err != nil {
			continue
		}
		chain[authID] = authEvent
		r.buildAuthChainMap(authEvent, chain)
	}
}

// isControlEvent 判断事件是否为"控制事件"（影响房间权限结构的事件）
// 控制事件：m.room.power_levels, m.room.join_rules, kick/ban 类型的 m.room.member
func isControlEvent(event *MatrixEvent) bool {
	switch event.EventType {
	case EventTypeRoomPowerLevels:
		return true
	case EventTypeRoomJoinRules:
		return event.StateKey != nil && *event.StateKey == ""
	case EventTypeRoomMember:
		if event.StateKey == nil {
			return false
		}
		// kick (leave by another) or ban
		membership, _ := event.Content["membership"].(string)
		if membership == "ban" {
			return true
		}
		if membership == "leave" && event.Sender != *event.StateKey {
			return true
		}
		return false
	default:
		return false
	}
}

// classifyEvents 将 conflicted events 分为 control events 和 other events
func (r *StateResolver) classifyEvents(allConflicted map[string]*MatrixEvent) (control, other []*MatrixEvent) {
	for _, event := range allConflicted {
		if isControlEvent(event) {
			control = append(control, event)
		} else {
			other = append(other, event)
		}
	}
	return
}

// reverseTopologicalOrdering 使用 Kahn's algorithm 按 auth event 依赖关系进行反向拓扑排序。
// 同一层级内的事件按 (sender power level desc, origin_server_ts asc, event_id asc) 排序。
// isControl 为 true 时使用 power level 排序，为 false 时使用 mainline 排序（调用方自行处理）。
func (r *StateResolver) reverseTopologicalOrdering(events []*MatrixEvent, isControl bool) []*MatrixEvent {
	if len(events) == 0 {
		return nil
	}

	// 构建事件 ID 集合
	eventSet := make(map[string]*MatrixEvent, len(events))
	for _, e := range events {
		eventSet[e.EventID] = e
	}

	// 计算入度：每个事件有多少个 auth event 在 eventSet 中（即依赖数）
	// 入度为 0 的事件是最基础的（没有依赖），应该最先处理
	inDegree := make(map[string]int, len(events))
	for _, e := range events {
		count := 0
		for _, authID := range e.AuthEvents {
			if _, ok := eventSet[authID]; ok {
				count++
			}
		}
		inDegree[e.EventID] = count
	}

	// 收集入度为 0 的事件作为起始
	var queue []*MatrixEvent
	for _, e := range events {
		if inDegree[e.EventID] == 0 {
			queue = append(queue, e)
		}
	}

	// 排序 queue 以保证确定性
	r.sortControlEvents(queue, eventSet)

	var result []*MatrixEvent
	for len(queue) > 0 {
		// 从 queue 中取出第一个（已排序）
		event := queue[0]
		queue = queue[1:]
		result = append(result, event)

		// 减少依赖于该事件的其他事件的入度
		for _, other := range events {
			if other.EventID == event.EventID {
				continue
			}
			for _, authID := range other.AuthEvents {
				if authID == event.EventID {
					inDegree[other.EventID]--
					if inDegree[other.EventID] == 0 {
						queue = append(queue, other)
					}
					break // 每个 auth event 只计一次
				}
			}
		}

		// 重新排序 queue
		r.sortControlEvents(queue, eventSet)
	}

	return result
}

// sortControlEvents 对控制事件排序：sender power level desc, origin_server_ts asc, event_id asc
func (r *StateResolver) sortControlEvents(events []*MatrixEvent, allEvents map[string]*MatrixEvent) {
	// 缓存 sender power level
	powerLevelCache := make(map[string]int64)
	getPowerLevel := func(event *MatrixEvent) int64 {
		if cached, ok := powerLevelCache[event.EventID]; ok {
			return cached
		}
		level := r.getSenderPowerLevelFromAuth(event, allEvents)
		powerLevelCache[event.EventID] = level
		return level
	}

	sort.SliceStable(events, func(i, j int) bool {
		plI := getPowerLevel(events[i])
		plJ := getPowerLevel(events[j])
		if plI != plJ {
			return plI > plJ // 高权限优先
		}
		if events[i].OriginServerTS != events[j].OriginServerTS {
			return events[i].OriginServerTS < events[j].OriginServerTS // 早时间优先
		}
		return events[i].EventID < events[j].EventID // 字典序
	})
}

// getSenderPowerLevelFromAuth 从事件的 auth events 中获取 sender 的 power level
func (r *StateResolver) getSenderPowerLevelFromAuth(event *MatrixEvent, allEvents map[string]*MatrixEvent) int64 {
	for _, authID := range event.AuthEvents {
		authEvent := allEvents[authID]
		if authEvent == nil {
			authEvent, _ = r.loadEvent(authID)
		}
		if authEvent == nil {
			continue
		}
		if authEvent.EventType == EventTypeRoomPowerLevels && authEvent.StateKey != nil && *authEvent.StateKey == "" {
			pl, err := parsePowerLevelContent(authEvent.Content)
			if err != nil {
				continue
			}
			if level, ok := pl.Users[event.Sender]; ok {
				return level
			}
			return pl.UsersDefault
		}
	}
	return 0 // 默认 power level
}

// buildStateMap 将 StateEntry 列表转换为 map
func (r *StateResolver) buildStateMap(entries []StateEntry) map[StateKeyTuple]StateEntry {
	m := make(map[StateKeyTuple]StateEntry, len(entries))
	for _, entry := range entries {
		m[entry.Tuple] = entry
	}
	return m
}

// authAndApplyEvents 逐一检查事件是否通过 auth check，通过则应用到 partial state
func (r *StateResolver) authAndApplyEvents(events []*MatrixEvent, partialState map[StateKeyTuple]StateEntry, allEvents map[string]*MatrixEvent) {
	for _, event := range events {
		if !event.IsStateEvent() {
			continue
		}

		// 从 partial state 构建 auth chain（简化：用 partial state 中的事件做 auth check）
		authChain := r.buildAuthChainFromState(event, partialState, allEvents)

		// Auth check
		if err := r.CheckAuthRules(event, authChain); err != nil {
			// Auth check 失败，跳过该事件
			continue
		}

		// 应用到 partial state
		tuple := StateKeyTuple{
			EventType: string(event.EventType),
			StateKey:  "",
		}
		if event.StateKey != nil {
			tuple.StateKey = *event.StateKey
		}
		partialState[tuple] = StateEntry{
			Tuple:   tuple,
			EventID: event.EventID,
			Event:   event,
		}
	}
}

// buildAuthChainFromState 从 partial state 中收集事件用于 auth check
func (r *StateResolver) buildAuthChainFromState(event *MatrixEvent, partialState map[StateKeyTuple]StateEntry, allEvents map[string]*MatrixEvent) []*MatrixEvent {
	var chain []*MatrixEvent

	// 总是包含 m.room.create
	if entry, ok := partialState[StateKeyTuple{string(EventTypeRoomCreate), ""}]; ok && entry.Event != nil {
		chain = append(chain, entry.Event)
	}

	// 总是包含 m.room.power_levels
	if entry, ok := partialState[StateKeyTuple{string(EventTypeRoomPowerLevels), ""}]; ok && entry.Event != nil {
		chain = append(chain, entry.Event)
	}

	// 包含 sender 的 m.room.member
	if entry, ok := partialState[StateKeyTuple{string(EventTypeRoomMember), event.Sender}]; ok && entry.Event != nil {
		chain = append(chain, entry.Event)
	}

	// 对于 m.room.member 事件，包含 m.room.join_rules
	if event.EventType == EventTypeRoomMember {
		if entry, ok := partialState[StateKeyTuple{string(EventTypeRoomJoinRules), ""}]; ok && entry.Event != nil {
			chain = append(chain, entry.Event)
		}
	}

	return chain
}

// createPowerLevelMainline 创建 power level mainline：从当前 power levels 事件追溯到 room creation
func (r *StateResolver) createPowerLevelMainline(partialState map[StateKeyTuple]StateEntry, allEvents map[string]*MatrixEvent) []*MatrixEvent {
	var mainline []*MatrixEvent

	// 从当前 partial state 中的 power levels 开始
	plEntry, ok := partialState[StateKeyTuple{string(EventTypeRoomPowerLevels), ""}]
	if !ok || plEntry.Event == nil {
		return mainline
	}

	current := plEntry.Event
	visited := make(map[string]bool)

	for current != nil && !visited[current.EventID] {
		visited[current.EventID] = true
		mainline = append(mainline, current)

		// 找到 current 的 auth event 中的 power level event
		var nextPL *MatrixEvent
		for _, authID := range current.AuthEvents {
			authEvent := allEvents[authID]
			if authEvent == nil {
				authEvent, _ = r.loadEvent(authID)
			}
			if authEvent != nil && authEvent.EventType == EventTypeRoomPowerLevels {
				nextPL = authEvent
				break
			}
		}
		current = nextPL
	}

	return mainline
}

// mainlineOrdering 按 mainline position 排序 other events
func (r *StateResolver) mainlineOrdering(events []*MatrixEvent, mainline []*MatrixEvent, allEvents map[string]*MatrixEvent) []*MatrixEvent {
	if len(events) == 0 {
		return nil
	}

	// 构建 mainline position map（越后面的 position 越大，表示越新）
	mainlinePos := make(map[string]int, len(mainline))
	for i, e := range mainline {
		mainlinePos[e.EventID] = len(mainline) - i // 反转，使较新的 PL 有更大的 position
	}

	// 为每个事件计算其最近的 mainline ancestor 的 position
	type eventWithMainlineInfo struct {
		event       *MatrixEvent
		mainlinePos int // 最近 mainline ancestor 的 position
		stepCount   int // 到 mainline 的距离
	}

	infos := make([]eventWithMainlineInfo, len(events))
	for i, event := range events {
		pos, steps := r.getMainlinePosition(event, mainlinePos, allEvents)
		infos[i] = eventWithMainlineInfo{
			event:       event,
			mainlinePos: pos,
			stepCount:   steps,
		}
	}

	// 排序: mainline position asc, step count asc, origin_server_ts asc, event_id asc
	sort.SliceStable(infos, func(i, j int) bool {
		if infos[i].mainlinePos != infos[j].mainlinePos {
			return infos[i].mainlinePos < infos[j].mainlinePos
		}
		if infos[i].stepCount != infos[j].stepCount {
			return infos[i].stepCount < infos[j].stepCount
		}
		if infos[i].event.OriginServerTS != infos[j].event.OriginServerTS {
			return infos[i].event.OriginServerTS < infos[j].event.OriginServerTS
		}
		return infos[i].event.EventID < infos[j].event.EventID
	})

	result := make([]*MatrixEvent, len(events))
	for i, info := range infos {
		result[i] = info.event
	}
	return result
}

// getMainlinePosition 查找事件最近的 mainline power level ancestor
func (r *StateResolver) getMainlinePosition(event *MatrixEvent, mainlinePos map[string]int, allEvents map[string]*MatrixEvent) (position int, steps int) {
	visited := make(map[string]bool)
	current := event
	step := 0

	for current != nil && !visited[current.EventID] {
		visited[current.EventID] = true

		// 检查 current 的 auth events 中是否有 mainline 上的 power level event
		for _, authID := range current.AuthEvents {
			if pos, ok := mainlinePos[authID]; ok {
				return pos, step
			}
		}

		// 向上追溯 auth events 中的 power level
		step++
		var nextPL *MatrixEvent
		for _, authID := range current.AuthEvents {
			authEvent := allEvents[authID]
			if authEvent == nil {
				authEvent, _ = r.loadEvent(authID)
			}
			if authEvent != nil && authEvent.EventType == EventTypeRoomPowerLevels {
				nextPL = authEvent
				break
			}
		}
		current = nextPL
	}

	return 0, step // 如果找不到 mainline ancestor，position 为 0
}

// ===== 保留原有的 DB/状态查询方法 =====

// ResolvePowerLevels 解析权力等级状态
func (r *StateResolver) ResolvePowerLevels(authChain []*MatrixEvent) (*PowerLevelContent, error) {
	var powerLevelEvent *MatrixEvent
	for _, event := range authChain {
		if event.EventType == EventTypeRoomPowerLevels {
			if event.StateKey != nil && *event.StateKey == "" {
				if powerLevelEvent == nil || event.Depth > powerLevelEvent.Depth {
					powerLevelEvent = event
				}
			}
		}
	}

	if powerLevelEvent == nil {
		// Matrix spec: if no m.room.power_levels exists, room creator has PL 100
		users := make(map[string]int64)
		for _, event := range authChain {
			if event.EventType == EventTypeRoomCreate {
				if creator, ok := event.Content["creator"].(string); ok && creator != "" {
					users[creator] = 100
				}
			}
		}
		return &PowerLevelContent{
			Users:         users,
			Events:        make(map[string]int64),
			UsersDefault:  0,
			EventsDefault: 0,
			StateDefault:  0,
			Ban:           50,
			Kick:          50,
			Redact:        50,
			Invite:        0,
		}, nil
	}

	return parsePowerLevelContent(powerLevelEvent.Content)
}

// loadEvent 从数据库或缓存加载事件
func (r *StateResolver) loadEvent(eventID string) (*MatrixEvent, error) {
	if e, ok := r.eventCache[eventID]; ok {
		return e, nil
	}
	if r.db == nil {
		return nil, fmt.Errorf("event not found: %s", eventID)
	}
	var event MatrixEvent
	if err := r.db.Where("event_id = ?", eventID).First(&event).Error; err != nil {
		return nil, err
	}
	r.eventCache[eventID] = &event
	return &event, nil
}

// LoadStateAtEvent 加载事件时的房间状态
func (r *StateResolver) LoadStateAtEvent(eventID string) ([]StateEntry, error) {
	event, err := r.loadEvent(eventID)
	if err != nil {
		return nil, fmt.Errorf("event not found: %w", err)
	}

	if event.IsStateEvent() {
		return r.loadStateBeforeEvent(event)
	}

	return r.loadCurrentState()
}

// loadStateBeforeEvent 加载事件之前的状态
func (r *StateResolver) loadStateBeforeEvent(event *MatrixEvent) ([]StateEntry, error) {
	if len(event.PrevEvents) == 0 {
		return r.loadCurrentState()
	}

	// 简化实现：返回当前状态
	return r.loadCurrentState()
}

// LoadCurrentState 加载当前房间状态（导出版本）
func (r *StateResolver) LoadCurrentState() ([]StateEntry, error) {
	return r.loadCurrentState()
}

// loadCurrentState 加载当前房间状态
func (r *StateResolver) loadCurrentState() ([]StateEntry, error) {
	if r.db == nil {
		return nil, nil
	}
	var states []MatrixRoomState
	if err := r.db.Where("room_id = ?", r.roomID).Find(&states).Error; err != nil {
		return nil, err
	}

	entries := make([]StateEntry, len(states))
	for i, state := range states {
		entries[i] = StateEntry{
			Tuple: StateKeyTuple{
				EventType: string(state.EventType),
				StateKey:  state.StateKey,
			},
			EventID: state.EventID,
		}
	}

	return entries, nil
}

// maxAuthChainDepth 认证链最大递归深度
const maxAuthChainDepth = 100

// BuildAuthChain 构建事件的认证链
func (r *StateResolver) BuildAuthChain(event *MatrixEvent) ([]*MatrixEvent, error) {
	authChain := make([]*MatrixEvent, 0)
	visited := make(map[string]bool)

	err := r.buildAuthChainRecursive(event, &authChain, visited, 0)
	if err != nil {
		return nil, err
	}

	return authChain, nil
}

// buildAuthChainRecursive 递归构建认证链
func (r *StateResolver) buildAuthChainRecursive(event *MatrixEvent, chain *[]*MatrixEvent, visited map[string]bool, depth int) error {
	if event == nil || visited[event.EventID] {
		return nil
	}

	if depth > maxAuthChainDepth {
		return fmt.Errorf("auth chain depth exceeded maximum of %d", maxAuthChainDepth)
	}

	visited[event.EventID] = true
	*chain = append(*chain, event)

	for _, authEventID := range event.AuthEvents {
		if visited[authEventID] {
			continue
		}

		authEvent, err := r.loadEvent(authEventID)
		if err != nil {
			continue
		}

		if err := r.buildAuthChainRecursive(authEvent, chain, visited, depth+1); err != nil {
			return err
		}
	}

	return nil
}

// CheckAuthRules 检查事件是否符合认证规则
func (r *StateResolver) CheckAuthRules(event *MatrixEvent, authChain []*MatrixEvent) error {
	powerLevels, err := r.ResolvePowerLevels(authChain)
	if err != nil {
		return fmt.Errorf("failed to resolve power levels: %w", err)
	}

	senderLevel := r.getUserPowerLevel(event.Sender, powerLevels)

	switch event.EventType {
	case EventTypeRoomCreate:
		return nil

	case EventTypeRoomPowerLevels:
		requiredLevel := r.getPowerLevelRequirement("m.room.power_levels", powerLevels)
		if senderLevel < requiredLevel {
			return fmt.Errorf("insufficient power level: got %d, need %d", senderLevel, requiredLevel)
		}
		// Matrix spec: sender must have PL >= both old and new values for every changed key
		if err := r.checkPowerLevelChanges(event, powerLevels, senderLevel); err != nil {
			return err
		}

	case EventTypeRoomMember:
		if err := r.checkMembershipAuth(event, authChain, powerLevels); err != nil {
			return err
		}

	default:
		if event.IsStateEvent() {
			requiredLevel := r.getPowerLevelRequirement(string(event.EventType), powerLevels)
			if senderLevel < requiredLevel {
				return fmt.Errorf("insufficient power level for state event: got %d, need %d", senderLevel, requiredLevel)
			}
		}
	}

	return nil
}

// checkPowerLevelChanges validates that the sender has sufficient PL to make each change
// in a m.room.power_levels event. Per the Matrix spec, the sender must have PL >= both
// the old value and the new value for every key being changed.
func (r *StateResolver) checkPowerLevelChanges(event *MatrixEvent, currentPL *PowerLevelContent, senderLevel int64) error {
	newPL, err := parsePowerLevelContent(event.Content)
	if err != nil {
		return fmt.Errorf("invalid power level content: %w", err)
	}

	// Check changes in users map
	allUsers := make(map[string]bool)
	for u := range currentPL.Users {
		allUsers[u] = true
	}
	for u := range newPL.Users {
		allUsers[u] = true
	}
	for user := range allUsers {
		oldVal := currentPL.UsersDefault
		if v, ok := currentPL.Users[user]; ok {
			oldVal = v
		}
		newVal := newPL.UsersDefault
		if v, ok := newPL.Users[user]; ok {
			newVal = v
		}
		if oldVal != newVal {
			if senderLevel < oldVal {
				return fmt.Errorf("cannot change user %s PL from %d: sender PL %d too low", user, oldVal, senderLevel)
			}
			if senderLevel < newVal {
				return fmt.Errorf("cannot set user %s PL to %d: sender PL %d too low", user, newVal, senderLevel)
			}
		}
	}

	// Check changes in scalar fields
	type plField struct {
		name   string
		oldVal int64
		newVal int64
	}
	fields := []plField{
		{"users_default", currentPL.UsersDefault, newPL.UsersDefault},
		{"events_default", currentPL.EventsDefault, newPL.EventsDefault},
		{"state_default", currentPL.StateDefault, newPL.StateDefault},
		{"ban", currentPL.Ban, newPL.Ban},
		{"kick", currentPL.Kick, newPL.Kick},
		{"redact", currentPL.Redact, newPL.Redact},
		{"invite", currentPL.Invite, newPL.Invite},
	}
	for _, f := range fields {
		if f.oldVal != f.newVal {
			if senderLevel < f.oldVal {
				return fmt.Errorf("cannot change %s from %d: sender PL %d too low", f.name, f.oldVal, senderLevel)
			}
			if senderLevel < f.newVal {
				return fmt.Errorf("cannot set %s to %d: sender PL %d too low", f.name, f.newVal, senderLevel)
			}
		}
	}

	// Check changes in events map
	allEventTypes := make(map[string]bool)
	for e := range currentPL.Events {
		allEventTypes[e] = true
	}
	for e := range newPL.Events {
		allEventTypes[e] = true
	}
	for et := range allEventTypes {
		oldVal := currentPL.EventsDefault
		if v, ok := currentPL.Events[et]; ok {
			oldVal = v
		}
		newVal := newPL.EventsDefault
		if v, ok := newPL.Events[et]; ok {
			newVal = v
		}
		if oldVal != newVal {
			if senderLevel < oldVal {
				return fmt.Errorf("cannot change events[%s] from %d: sender PL %d too low", et, oldVal, senderLevel)
			}
			if senderLevel < newVal {
				return fmt.Errorf("cannot set events[%s] to %d: sender PL %d too low", et, newVal, senderLevel)
			}
		}
	}

	return nil
}

// checkMembershipAuth 检查成员变更权限
func (r *StateResolver) checkMembershipAuth(event *MatrixEvent, authChain []*MatrixEvent, powerLevels *PowerLevelContent) error {
	if event.StateKey == nil {
		return fmt.Errorf("membership event must have state_key")
	}

	targetUser := *event.StateKey
	membership, _ := event.Content["membership"].(string)

	currentMembership := r.getCurrentMembership(targetUser, authChain)

	senderLevel := r.getUserPowerLevel(event.Sender, powerLevels)
	targetLevel := r.getUserPowerLevel(targetUser, powerLevels)

	switch membership {
	case "invite":
		if senderLevel < powerLevels.Invite {
			return fmt.Errorf("insufficient power level to invite: got %d, need %d", senderLevel, powerLevels.Invite)
		}
		if targetLevel > senderLevel {
			return fmt.Errorf("cannot invite user with higher power level")
		}

	case "join":
		// Matrix spec: banned users cannot join
		if currentMembership == "ban" {
			return fmt.Errorf("user is banned")
		}
		if currentMembership != "invite" && currentMembership != "join" {
			joinRule := r.getJoinRule(authChain)
			if joinRule == "invite" {
				return fmt.Errorf("user is not invited")
			}
		}

	case "leave":
		if event.Sender == targetUser {
			return nil
		}
		if senderLevel < powerLevels.Kick {
			return fmt.Errorf("insufficient power level to kick: got %d, need %d", senderLevel, powerLevels.Kick)
		}
		if targetLevel >= senderLevel {
			return fmt.Errorf("cannot kick user with equal or higher power level")
		}

	case "ban":
		if senderLevel < powerLevels.Ban {
			return fmt.Errorf("insufficient power level to ban: got %d, need %d", senderLevel, powerLevels.Ban)
		}
		if targetLevel >= senderLevel {
			return fmt.Errorf("cannot ban user with equal or higher power level")
		}
	}

	return nil
}

// getUserPowerLevel 获取用户权力等级
func (r *StateResolver) getUserPowerLevel(userID string, powerLevels *PowerLevelContent) int64 {
	if level, ok := powerLevels.Users[userID]; ok {
		return level
	}
	return powerLevels.UsersDefault
}

// getPowerLevelRequirement 获取状态事件所需的权力等级
func (r *StateResolver) getPowerLevelRequirement(eventType string, powerLevels *PowerLevelContent) int64 {
	if level, ok := powerLevels.Events[eventType]; ok {
		return level
	}
	return powerLevels.StateDefault
}

// getCurrentMembership 获取用户当前成员状态
func (r *StateResolver) getCurrentMembership(userID string, authChain []*MatrixEvent) string {
	for i := len(authChain) - 1; i >= 0; i-- {
		event := authChain[i]
		if event.EventType == EventTypeRoomMember && event.StateKey != nil && *event.StateKey == userID {
			if membership, ok := event.Content["membership"].(string); ok {
				return membership
			}
		}
	}
	return "leave"
}

// getJoinRule 获取房间加入规则
func (r *StateResolver) getJoinRule(authChain []*MatrixEvent) string {
	for i := len(authChain) - 1; i >= 0; i-- {
		event := authChain[i]
		if event.EventType == EventTypeRoomJoinRules && event.StateKey != nil && *event.StateKey == "" {
			if joinRule, ok := event.Content["join_rule"].(string); ok {
				return joinRule
			}
		}
	}
	return "invite"
}

// ===== 辅助函数 =====

// parsePowerLevelContent 解析权力等级内容
func parsePowerLevelContent(content map[string]interface{}) (*PowerLevelContent, error) {
	data, err := json.Marshal(content)
	if err != nil {
		return nil, err
	}

	var plc PowerLevelContent
	if err := json.Unmarshal(data, &plc); err != nil {
		return nil, err
	}

	if plc.Users == nil {
		plc.Users = make(map[string]int64)
	}
	if plc.Events == nil {
		plc.Events = make(map[string]int64)
	}

	return &plc, nil
}

// ComputeStateDiff 计算两个状态集合的差异
func ComputeStateDiff(oldState, newState []StateEntry) (added, removed, modified []StateEntry) {
	oldMap := make(map[StateKeyTuple]StateEntry)
	for _, entry := range oldState {
		oldMap[entry.Tuple] = entry
	}

	newMap := make(map[StateKeyTuple]StateEntry)
	for _, entry := range newState {
		newMap[entry.Tuple] = entry
	}

	for tuple, newEntry := range newMap {
		if oldEntry, ok := oldMap[tuple]; !ok {
			added = append(added, newEntry)
		} else if oldEntry.EventID != newEntry.EventID {
			modified = append(modified, newEntry)
		}
	}

	for tuple, oldEntry := range oldMap {
		if _, ok := newMap[tuple]; !ok {
			removed = append(removed, oldEntry)
		}
	}

	return
}
