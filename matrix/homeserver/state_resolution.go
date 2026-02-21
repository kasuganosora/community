package homeserver

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
)

// ===== 状态解析算法（基于 Dendrite 实现）=====

// StateResolver 状态解析器
type StateResolver struct {
	db       *gorm.DB
	roomID   string
	roomInfo *MatrixRoom
}

// NewStateResolver 创建状态解析器
func NewStateResolver(roomID string) *StateResolver {
	return &StateResolver{
		db:     db.GetDB(),
		roomID: roomID,
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

// ResolveState 解析状态（处理多个状态集合的冲突）
func (r *StateResolver) ResolveState(stateSets [][]StateEntry) ([]StateEntry, error) {
	if len(stateSets) == 0 {
		return nil, nil
	}

	// 如果只有一个状态集合，直接返回
	if len(stateSets) == 1 {
		return stateSets[0], nil
	}

	// 收集所有唯一的状态键
	allTuples := make(map[StateKeyTuple]bool)
	for _, stateSet := range stateSets {
		for _, entry := range stateSet {
			allTuples[entry.Tuple] = true
		}
	}

	// 为每个状态键解析冲突
	resolved := make([]StateEntry, 0, len(allTuples))
	for tuple := range allTuples {
		// 收集该状态键的所有事件
		var candidates []*MatrixEvent
		for _, stateSet := range stateSets {
			for _, entry := range stateSet {
				if entry.Tuple == tuple {
					if entry.Event != nil {
						candidates = append(candidates, entry.Event)
					} else if entry.EventID != "" {
						// 加载事件
						if event, err := r.loadEvent(entry.EventID); err == nil {
							candidates = append(candidates, event)
						}
					}
					break // 每个状态集合中每个键只有一个事件
				}
			}
		}

		// 解析冲突
		if len(candidates) > 0 {
			winner := r.resolveConflict(candidates)
			resolved = append(resolved, StateEntry{
				Tuple:   tuple,
				EventID: winner.EventID,
				Event:   winner,
			})
		}
	}

	return resolved, nil
}

// ResolvePowerLevels 解析权力等级状态
func (r *StateResolver) ResolvePowerLevels(authChain []*MatrixEvent) (*PowerLevelContent, error) {
	// 在认证链中查找 m.room.power_levels 事件
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
		// 返回默认权力等级
		return &PowerLevelContent{
			Users:         make(map[string]int64),
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

	// 解析权力等级内容
	return parsePowerLevelContent(powerLevelEvent.Content)
}

// resolveConflict 解析事件冲突（使用 Matrix 状态解析算法）
func (r *StateResolver) resolveConflict(events []*MatrixEvent) *MatrixEvent {
	if len(events) == 0 {
		return nil
	}
	if len(events) == 1 {
		return events[0]
	}

	// 排序事件（按深度、时间戳、事件ID字典序）
	sort.Slice(events, func(i, j int) bool {
		// 首先按深度排序（较大的深度优先）
		if events[i].Depth != events[j].Depth {
			return events[i].Depth > events[j].Depth
		}

		// 然后按时间戳排序（较大的时间戳优先）
		if events[i].OriginServerTS != events[j].OriginServerTS {
			return events[i].OriginServerTS > events[j].OriginServerTS
		}

		// 最后按事件ID字典序排序（较小的字典序优先）
		return events[i].EventID < events[j].EventID
	})

	// 返回排序后的第一个事件（胜出者）
	return events[0]
}

// loadEvent 从数据库加载事件
func (r *StateResolver) loadEvent(eventID string) (*MatrixEvent, error) {
	var event MatrixEvent
	if err := r.db.Where("event_id = ?", eventID).First(&event).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

// LoadStateAtEvent 加载事件时的房间状态
func (r *StateResolver) LoadStateAtEvent(eventID string) ([]StateEntry, error) {
	// 获取事件
	event, err := r.loadEvent(eventID)
	if err != nil {
		return nil, fmt.Errorf("event not found: %w", err)
	}

	// 对于状态事件，返回其前序事件的状态
	if event.IsStateEvent() {
		return r.loadStateBeforeEvent(event)
	}

	// 对于消息事件，返回当前房间状态
	return r.loadCurrentState()
}

// loadStateBeforeEvent 加载事件之前的状态
func (r *StateResolver) loadStateBeforeEvent(event *MatrixEvent) ([]StateEntry, error) {
	// 获取前序事件
	if len(event.PrevEvents) == 0 {
		return r.loadCurrentState()
	}

	// 简化实现：返回当前状态
	// 实际实现应该递归构建状态
	return r.loadCurrentState()
}

// loadCurrentState 加载当前房间状态
func (r *StateResolver) loadCurrentState() ([]StateEntry, error) {
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

// BuildAuthChain 构建事件的认证链
func (r *StateResolver) BuildAuthChain(event *MatrixEvent) ([]*MatrixEvent, error) {
	authChain := make([]*MatrixEvent, 0)
	visited := make(map[string]bool)

	// 从当前事件开始递归构建
	err := r.buildAuthChainRecursive(event, &authChain, visited)
	if err != nil {
		return nil, err
	}

	return authChain, nil
}

// buildAuthChainRecursive 递归构建认证链
func (r *StateResolver) buildAuthChainRecursive(event *MatrixEvent, chain *[]*MatrixEvent, visited map[string]bool) error {
	if event == nil || visited[event.EventID] {
		return nil
	}

	visited[event.EventID] = true
	*chain = append(*chain, event)

	// 递归处理 auth_events
	for _, authEventID := range event.AuthEvents {
		if visited[authEventID] {
			continue
		}

		authEvent, err := r.loadEvent(authEventID)
		if err != nil {
			// 某些 auth_events 可能不存在，继续处理
			continue
		}

		if err := r.buildAuthChainRecursive(authEvent, chain, visited); err != nil {
			return err
		}
	}

	return nil
}

// CheckAuthRules 检查事件是否符合认证规则
func (r *StateResolver) CheckAuthRules(event *MatrixEvent, authChain []*MatrixEvent) error {
	// 获取当前权力等级
	powerLevels, err := r.ResolvePowerLevels(authChain)
	if err != nil {
		return fmt.Errorf("failed to resolve power levels: %w", err)
	}

	// 检查发送者权限
	senderLevel := r.getUserPowerLevel(event.Sender, powerLevels)

	// 根据事件类型检查权限
	switch event.EventType {
	case EventTypeRoomCreate:
		// 只有创建事件可以在没有前序事件时发送
		return nil

	case EventTypeRoomPowerLevels:
		// 需要当前权力等级 >= 当前 power_levels 事件的等级
		requiredLevel := r.getPowerLevelRequirement("m.room.power_levels", powerLevels)
		if senderLevel < requiredLevel {
			return fmt.Errorf("insufficient power level: got %d, need %d", senderLevel, requiredLevel)
		}

	case EventTypeRoomMember:
		// 检查成员变更权限
		if err := r.checkMembershipAuth(event, authChain, powerLevels); err != nil {
			return err
		}

	default:
		// 检查状态事件默认权限
		if event.IsStateEvent() {
			requiredLevel := r.getPowerLevelRequirement(string(event.EventType), powerLevels)
			if senderLevel < requiredLevel {
				return fmt.Errorf("insufficient power level for state event: got %d, need %d", senderLevel, requiredLevel)
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

	// 获取目标用户的当前成员状态
	currentMembership := r.getCurrentMembership(targetUser, authChain)

	// 获取发送者和目标用户的权力等级
	senderLevel := r.getUserPowerLevel(event.Sender, powerLevels)
	targetLevel := r.getUserPowerLevel(targetUser, powerLevels)

	switch membership {
	case "invite":
		// 需要邀请权限
		if senderLevel < powerLevels.Invite {
			return fmt.Errorf("insufficient power level to invite: got %d, need %d", senderLevel, powerLevels.Invite)
		}
		// 不能邀请权力等级比自己高的人
		if targetLevel > senderLevel {
			return fmt.Errorf("cannot invite user with higher power level")
		}

	case "join":
		// 检查是否可以加入
		if currentMembership != "invite" && currentMembership != "join" {
			// 需要检查加入规则
			joinRule := r.getJoinRule(authChain)
			if joinRule == "invite" {
				return fmt.Errorf("user is not invited")
			}
		}

	case "leave":
		// 可以让自己离开
		if event.Sender == targetUser {
			return nil
		}
		// 需要踢出权限
		if senderLevel < powerLevels.Kick {
			return fmt.Errorf("insufficient power level to kick: got %d, need %d", senderLevel, powerLevels.Kick)
		}
		// 不能踢出权力等级 >= 自己的人
		if targetLevel >= senderLevel {
			return fmt.Errorf("cannot kick user with equal or higher power level")
		}

	case "ban":
		// 需要封禁权限
		if senderLevel < powerLevels.Ban {
			return fmt.Errorf("insufficient power level to ban: got %d, need %d", senderLevel, powerLevels.Ban)
		}
		// 不能封禁权力等级 >= 自己的人
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
	// 在认证链中查找最新的成员状态
	for i := len(authChain) - 1; i >= 0; i-- {
		event := authChain[i]
		if event.EventType == EventTypeRoomMember && event.StateKey != nil && *event.StateKey == userID {
			if membership, ok := event.Content["membership"].(string); ok {
				return membership
			}
		}
	}
	return "leave" // 默认状态
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
	return "invite" // 默认规则
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

	// 设置默认值
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

	// 找出新增和修改的
	for tuple, newEntry := range newMap {
		if oldEntry, ok := oldMap[tuple]; !ok {
			added = append(added, newEntry)
		} else if oldEntry.EventID != newEntry.EventID {
			modified = append(modified, newEntry)
		}
	}

	// 找出移除的
	for tuple, oldEntry := range oldMap {
		if _, ok := newMap[tuple]; !ok {
			removed = append(removed, oldEntry)
		}
	}

	return
}
