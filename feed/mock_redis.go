package feed

import (
	"context"
	"sync"

	"github.com/redis/go-redis/v9"
)

// MockRedisClient 是用于测试的 Redis 客户端 mock 实现
type MockRedisClient struct {
	mu sync.RWMutex
	// 存储有序集合数据
	zSets map[string]map[string]float64 // key -> member -> score
	// 存储集合数据
	sets map[string]map[string]struct{}
	// 存储普通键值
	kv map[string]string
	// 存储发布/订阅频道
	channels map[string]bool
	// 存储时间线更新时间
	timelines map[string]float64
}

// NewMockRedisClient 创建新的 MockRedisClient
func NewMockRedisClient() *MockRedisClient {
	return &MockRedisClient{
		zSets:     make(map[string]map[string]float64),
		sets:      make(map[string]map[string]struct{}),
		kv:        make(map[string]string),
		channels:  make(map[string]bool),
		timelines: make(map[string]float64),
	}
}

// ZAdd 添加有序集合成员
func (m *MockRedisClient) ZAdd(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd := redis.NewIntCmd(ctx)
	if m.zSets[key] == nil {
		m.zSets[key] = make(map[string]float64)
	}
	var added int64
	for _, z := range members {
		member := z.Member.(string)
		if _, exists := m.zSets[key][member]; !exists {
			added++
		}
		m.zSets[key][member] = z.Score
	}
	cmd.SetVal(added)
	return cmd
}

// ZRem 移除有序集合成员
func (m *MockRedisClient) ZRem(ctx context.Context, key string, members ...any) *redis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd := redis.NewIntCmd(ctx)
	if m.zSets[key] == nil {
		cmd.SetVal(0)
		return cmd
	}
	var removed int64
	for _, member := range members {
		memberStr := member.(string)
		if _, exists := m.zSets[key][memberStr]; exists {
			delete(m.zSets[key], memberStr)
			removed++
		}
	}
	cmd.SetVal(removed)
	return cmd
}

// ZScore 获取有序集合成员分数
func (m *MockRedisClient) ZScore(ctx context.Context, key, member string) *redis.FloatCmd {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cmd := redis.NewFloatCmd(ctx)
	if m.zSets[key] == nil {
		cmd.SetErr(redis.Nil)
		return cmd
	}
	if score, exists := m.zSets[key][member]; exists {
		cmd.SetVal(score)
	} else {
		cmd.SetVal(0)
	}
	return cmd
}

// ZRevRank 获取有序集合成员排名（从高到低）
func (m *MockRedisClient) ZRevRank(ctx context.Context, key string, member string) *redis.IntCmd {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cmd := redis.NewIntCmd(ctx)
	if m.zSets[key] == nil {
		cmd.SetErr(redis.Nil)
		return cmd
	}

	if _, exists := m.zSets[key][member]; !exists {
		cmd.SetErr(redis.Nil)
		return cmd
	}

	// 计算排名：按分数从高到低排序
	type memberScore struct {
		member string
		score  float64
	}
	var sortedMembers []memberScore
	for m, s := range m.zSets[key] {
		sortedMembers = append(sortedMembers, memberScore{member: m, score: s})
	}

	// 按分数降序排序
	for i := 0; i < len(sortedMembers); i++ {
		for j := i + 1; j < len(sortedMembers); j++ {
			if sortedMembers[i].score < sortedMembers[j].score {
				sortedMembers[i], sortedMembers[j] = sortedMembers[j], sortedMembers[i]
			}
		}
	}

	// 查找成员的排名
	for i, ms := range sortedMembers {
		if ms.member == member {
			cmd.SetVal(int64(i))
			return cmd
		}
	}

	cmd.SetErr(redis.Nil)
	return cmd
}

// ZRange 获取有序集合范围
func (m *MockRedisClient) ZRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cmd := redis.NewStringSliceCmd(ctx)
	if m.zSets[key] == nil {
		cmd.SetVal([]string{})
		return cmd
	}

	// 将成员按分数升序排序
	type memberScore struct {
		member string
		score  float64
	}
	var sortedMembers []memberScore
	for m, s := range m.zSets[key] {
		sortedMembers = append(sortedMembers, memberScore{member: m, score: s})
	}

	// 按分数升序排序
	for i := 0; i < len(sortedMembers); i++ {
		for j := i + 1; j < len(sortedMembers); j++ {
			if sortedMembers[i].score > sortedMembers[j].score {
				sortedMembers[i], sortedMembers[j] = sortedMembers[j], sortedMembers[i]
			}
		}
	}

	// 处理负索引
	n := len(sortedMembers)
	if start < 0 {
		start = int64(n) + start
	}
	if stop < 0 {
		stop = int64(n) + stop
	}

	// 边界检查
	if start < 0 {
		start = 0
	}
	if stop >= int64(n) {
		stop = int64(n) - 1
	}

	// 提取范围内的成员
	var result []string
	for i := start; i <= stop && i < int64(n) && i >= 0; i++ {
		result = append(result, sortedMembers[i].member)
	}

	cmd.SetVal(result)
	return cmd
}

// ZRangeByScore 按分数范围获取有序集合
func (m *MockRedisClient) ZRangeByScore(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.StringSliceCmd {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cmd := redis.NewStringSliceCmd(ctx)
	if m.zSets[key] == nil {
		cmd.SetVal([]string{})
		return cmd
	}

	// 简化实现：返回所有成员
	var result []string
	for member := range m.zSets[key] {
		result = append(result, member)
	}
	cmd.SetVal(result)
	return cmd
}

// ZRangeWithScores 按范围获取有序集合及分数
func (m *MockRedisClient) ZRangeWithScores(ctx context.Context, key string, start, stop int64) *redis.ZSliceCmd {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cmd := redis.NewZSliceCmd(ctx)
	if m.zSets[key] == nil {
		cmd.SetVal([]redis.Z{})
		return cmd
	}

	var result []redis.Z
	for member, score := range m.zSets[key] {
		result = append(result, redis.Z{Score: score, Member: member})
	}
	if len(result) > 0 {
		cmd.SetVal(result)
	}
	return cmd
}

// ZRevRange 反向获取有序集合范围
func (m *MockRedisClient) ZRevRange(ctx context.Context, key string, start, stop int64) *redis.StringSliceCmd {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cmd := redis.NewStringSliceCmd(ctx)
	if m.zSets[key] == nil {
		cmd.SetVal([]string{})
		return cmd
	}

	// 将成员按分数降序排序
	type memberScore struct {
		member string
		score  float64
	}
	var sortedMembers []memberScore
	for m, s := range m.zSets[key] {
		sortedMembers = append(sortedMembers, memberScore{member: m, score: s})
	}

	// 按分数降序排序
	for i := 0; i < len(sortedMembers); i++ {
		for j := i + 1; j < len(sortedMembers); j++ {
			if sortedMembers[i].score < sortedMembers[j].score {
				sortedMembers[i], sortedMembers[j] = sortedMembers[j], sortedMembers[i]
			}
		}
	}

	// 处理负索引
	n := len(sortedMembers)
	if start < 0 {
		start = int64(n) + start
	}
	if stop < 0 {
		stop = int64(n) + stop
	}

	// 边界检查
	if start < 0 {
		start = 0
	}
	if stop >= int64(n) {
		stop = int64(n) - 1
	}

	// 提取范围内的成员
	var result []string
	for i := start; i <= stop && i < int64(n) && i >= 0; i++ {
		result = append(result, sortedMembers[i].member)
	}

	cmd.SetVal(result)
	return cmd
}

// ZRemRangeByRank 按排名移除有序集合成员
func (m *MockRedisClient) ZRemRangeByRank(ctx context.Context, key string, start, stop int64) *redis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd := redis.NewIntCmd(ctx)

	if m.zSets[key] == nil {
		cmd.SetVal(0)
		return cmd
	}

	// 将 map 转换为按分数排序的切片
	type memberScore struct {
		member string
		score  float64
	}
	var sortedMembers []memberScore
	for member, score := range m.zSets[key] {
		sortedMembers = append(sortedMembers, memberScore{member: member, score: score})
	}

	// 按分数排序（升序）
	for i := 0; i < len(sortedMembers); i++ {
		for j := i + 1; j < len(sortedMembers); j++ {
			if sortedMembers[i].score > sortedMembers[j].score {
				sortedMembers[i], sortedMembers[j] = sortedMembers[j], sortedMembers[i]
			}
		}
	}

	// 计算要删除的范围
	n := len(sortedMembers)
	var removeStart, removeEnd int

	// 处理负索引
	if start < 0 {
		removeStart = n + int(start)
	} else {
		removeStart = int(start)
	}
	if stop < 0 {
		removeEnd = n + int(stop)
	} else {
		removeEnd = int(stop)
	}

	// 边界检查
	if removeStart < 0 {
		removeStart = 0
	}
	if removeEnd >= n {
		removeEnd = n - 1
	}

	// 删除范围内的成员
	var removed int64
	for i := removeStart; i <= removeEnd && i < n; i++ {
		delete(m.zSets[key], sortedMembers[i].member)
		removed++
	}

	cmd.SetVal(removed)
	return cmd
}

// ZAddNX 仅当成员不存在时添加
func (m *MockRedisClient) ZAddNX(ctx context.Context, key string, members ...redis.Z) *redis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd := redis.NewIntCmd(ctx)
	if m.zSets[key] == nil {
		m.zSets[key] = make(map[string]float64)
	}
	var added int64
	for _, z := range members {
		member := z.Member.(string)
		if _, exists := m.zSets[key][member]; !exists {
			m.zSets[key][member] = z.Score
			added++
		}
	}
	cmd.SetVal(added)
	return cmd
}

// SAdd 添加集合成员
func (m *MockRedisClient) SAdd(ctx context.Context, key string, members ...any) *redis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd := redis.NewIntCmd(ctx)
	if m.sets[key] == nil {
		m.sets[key] = make(map[string]struct{})
	}
	var added int64
	for _, member := range members {
		memberStr := member.(string)
		if _, exists := m.sets[key][memberStr]; !exists {
			m.sets[key][memberStr] = struct{}{}
			added++
		}
	}
	cmd.SetVal(added)
	return cmd
}

// SRem 移除集合成员
func (m *MockRedisClient) SRem(ctx context.Context, key string, members ...any) *redis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd := redis.NewIntCmd(ctx)
	if m.sets[key] == nil {
		cmd.SetVal(0)
		return cmd
	}
	var removed int64
	for _, member := range members {
		memberStr := member.(string)
		if _, exists := m.sets[key][memberStr]; exists {
			delete(m.sets[key], memberStr)
			removed++
		}
	}
	cmd.SetVal(removed)
	return cmd
}

// SMembers 获取集合所有成员
func (m *MockRedisClient) SMembers(ctx context.Context, key string) *redis.StringSliceCmd {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cmd := redis.NewStringSliceCmd(ctx)
	if m.sets[key] == nil {
		cmd.SetVal([]string{})
		return cmd
	}
	var result []string
	for member := range m.sets[key] {
		result = append(result, member)
	}
	cmd.SetVal(result)
	return cmd
}

// Del 删除键
func (m *MockRedisClient) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd := redis.NewIntCmd(ctx)
	var deleted int64
	for _, key := range keys {
		if _, exists := m.zSets[key]; exists {
			delete(m.zSets, key)
			deleted++
		}
		if _, exists := m.sets[key]; exists {
			delete(m.sets, key)
			deleted++
		}
		if _, exists := m.kv[key]; exists {
			delete(m.kv, key)
			deleted++
		}
	}
	cmd.SetVal(deleted)
	return cmd
}

// Publish 发布消息
func (m *MockRedisClient) Publish(ctx context.Context, channel string, message any) *redis.IntCmd {
	cmd := redis.NewIntCmd(ctx)
	cmd.SetVal(1)
	return cmd
}

// PubSubChannels 获取活跃的发布/订阅频道
func (m *MockRedisClient) PubSubChannels(ctx context.Context, pattern string) *redis.StringSliceCmd {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cmd := redis.NewStringSliceCmd(ctx)
	var result []string
	for channel := range m.channels {
		result = append(result, channel)
	}
	cmd.SetVal(result)
	return cmd
}

// Pipelined 执行管道命令 - 简化实现
// 由于 redis.Pipeliner 接口有很多方法，我们使用一个通用的方法
func (m *MockRedisClient) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	// 简化实现：创建一个真实 Redis 客户端的 Pipeline 来执行操作
	// 但由于我们没有真实 Redis，所以只是忽略 Pipeline 操作
	// 在测试中，我们单独测试 Pipeline 相关的功能
	return []redis.Cmder{}, nil
}

// Keys 获取匹配的键
func (m *MockRedisClient) Keys(ctx context.Context, pattern string) *redis.StringSliceCmd {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cmd := redis.NewStringSliceCmd(ctx)
	// 简化实现
	cmd.SetVal([]string{})
	return cmd
}

// Unlink 异步删除键
func (m *MockRedisClient) Unlink(ctx context.Context, keys ...string) *redis.IntCmd {
	return m.Del(ctx, keys...)
}

// ZRevRangeByScoreWithScores 按分数反向获取有序集合及分数
func (m *MockRedisClient) ZRevRangeByScoreWithScores(ctx context.Context, key string, opt *redis.ZRangeBy) *redis.ZSliceCmd {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cmd := redis.NewZSliceCmd(ctx)
	if m.zSets[key] == nil {
		cmd.SetVal([]redis.Z{})
		return cmd
	}

	var result []redis.Z
	for member, score := range m.zSets[key] {
		result = append(result, redis.Z{Score: score, Member: member})
	}
	cmd.SetVal(result)
	return cmd
}

// ZRemRangeByScore 按分数移除有序集合成员
func (m *MockRedisClient) ZRemRangeByScore(ctx context.Context, key string, min, max string) *redis.IntCmd {
	m.mu.Lock()
	defer m.mu.Unlock()
	cmd := redis.NewIntCmd(ctx)
	// 简化实现
	cmd.SetVal(0)
	return cmd
}

// SetChannel 设置频道状态（用于测试）
func (m *MockRedisClient) SetChannel(channel string, active bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channels[channel] = active
}

// GetZSet 获取有序集合（用于测试验证）
func (m *MockRedisClient) GetZSet(key string) map[string]float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]float64)
	if m.zSets[key] != nil {
		for k, v := range m.zSets[key] {
			result[k] = v
		}
	}
	return result
}

// GetSet 获取集合（用于测试验证）
func (m *MockRedisClient) GetSet(key string) map[string]struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]struct{})
	if m.sets[key] != nil {
		for k, v := range m.sets[key] {
			result[k] = v
		}
	}
	return result
}

// Scan 非阻塞迭代键（Mock 实现）
func (m *MockRedisClient) Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 简化的 Scan 实现：返回所有匹配的键
	var keys []string
	for key := range m.zSets {
		if matchPattern(key, match) {
			keys = append(keys, key)
		}
	}
	for key := range m.sets {
		if matchPattern(key, match) {
			keys = append(keys, key)
		}
	}

	// 创建一个 ScanCmd，使用 nil 作为 cmdable（因为我们不需要实际的 Redis 调用）
	cmd := redis.NewScanCmd(ctx, nil, "scan", cursor, "match", match, "count", count)
	// 返回结果（cursor=0 表示结束）
	cmd.SetVal(keys, 0)
	return cmd
}

// matchPattern 简单的通配符匹配
func matchPattern(s, pattern string) bool {
	if pattern == "*" {
		return true
	}
	// 简化实现：仅支持前缀匹配
	if len(pattern) > 1 && pattern[len(pattern)-1] == '*' {
		prefix := pattern[:len(pattern)-1]
		return len(s) >= len(prefix) && s[:len(prefix)] == prefix
	}
	return s == pattern
}

// 确保 MockRedisClient 实现了 RedisClient 接口
var _ RedisClient = (*MockRedisClient)(nil)
