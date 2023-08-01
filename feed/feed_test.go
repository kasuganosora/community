package feed

import (
	"context"
	"fmt"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"github.com/stretchr/testify/assert"
	"os"
	"testing"
)

// 定义一些模拟数据，例如状态、时间线和用户
var (
	mockStatus1 = &models.Status{Id: 1, ReblogId: nil}
	mockStatus2 = &models.Status{Id: 2, ReblogId: nil}
	mockStatus3 = &models.Status{Id: 3, ReblogId: &mockStatus1.Id}
	mockStatus4 = &models.Status{Id: 4, ReblogId: &mockStatus1.Id}
	mockStatus5 = &models.Status{Id: 5, ReblogId: &mockStatus2.Id}
	mockStatus6 = &models.Status{Id: 6}
	mockStatus7 = &models.Status{Id: 7, ReblogId: &mockStatus6.Id}

	mockTimeline = &MockTimeline{Type: "home", Id: 1}

	mockUser1 uint64 = 1
	mockUser2 uint64 = 2
)

type MockTimeline struct {
	Type string
	Id   any
}

func (t *MockTimeline) GetKeyFormat() string {
	return "feed:" + t.Type + ":%s"
}

func (t *MockTimeline) GetTimelineId() string {
	return utils.ToString(t.Id)
}

func TestMain(m *testing.M) {
	// 测试前清空redis
	ctx := context.Background()
	redisConn := db.GetRedis()
	redisConn.FlushAll(ctx)

	// 运行测试
	exitCode := m.Run()

	// 清空redis
	//redisConn.FlushAll(ctx)

	// 退出
	os.Exit(exitCode)
}

func TestAddToTimelineItem(t *testing.T) {
	defer clearRedis()

	// 添加一些状态到时间线
	_ = AddToTimelineItem(mockTimeline, mockStatus1, true)
	_ = AddToTimelineItem(mockTimeline, mockStatus2, true)
	_ = AddToTimelineItem(mockTimeline, mockStatus3, true)

	// 检查对应redis 读取的值是否正确
	redisConn := db.GetRedis()
	ctx := context.Background()
	// 读取时间线
	timelineKeyFormat := mockTimeline.GetKeyFormat()
	timelineId := mockTimeline.GetTimelineId()
	timelineKey := fmt.Sprintf(timelineKeyFormat, timelineId)
	timeline, err := redisConn.ZRange(ctx, timelineKey, 0, -1).Result()
	assert.Nil(t, err)
	// 输出 2 1，没有 3，因为 3 是转发的，并且3的原始状态 1 已经在时间线中了（也在REBLOG_FALLOFF范围内）
	assert.EqualValuesf(t, []string{utils.ToString(mockStatus1.Id), utils.ToString(mockStatus2.Id)}, timeline, "timeline should be %v", timeline)

	// 测试转发没在timeline中的状态, mockStatus7 的原始状态 mockStatus6 不在时间线中，所以用例的输出应该 1 2 7
	_ = AddToTimelineItem(mockTimeline, mockStatus7, true)
	timeline, err = redisConn.ZRange(ctx, timelineKey, 0, -1).Result()
	assert.Nil(t, err)
	assert.EqualValuesf(t, []string{utils.ToString(mockStatus1.Id), utils.ToString(mockStatus2.Id), utils.ToString(mockStatus7.Id)}, timeline, "timeline should be %v", timeline)
	// 另外还回产生一个新reblogs集合（feed:home:1:reblogs），里面应该有 mockStatus6.Id -> mockStatus7.Id
	reblogsKey := fmt.Sprintf("feed:%s:%s:reblogs", mockTimeline.Type, timelineId)
	reblogs, err := redisConn.ZRangeWithScores(ctx, reblogsKey, 0, -1).Result()
	assert.Nil(t, err)
	assert.Len(t, reblogs, 1)
	assert.EqualValuesf(t, utils.ToString(mockStatus7.Id), utils.ToString(reblogs[0].Score), "reblogs should be %v", reblogs)
	assert.EqualValuesf(t, utils.ToString(mockStatus6.Id), reblogs[0].Member, "reblogs should be %v", reblogs)

	// 在同一条状态被多次连续转发的实况下，除了第一条会被记录到时间线上（feed:home:1), 和转推集合（feed:home:1:reblogs）中，其他的都会记录在 feed:home:1:reblogs:{reblogId} 中
	// 前提是这些转发都是在 REBLOG_FALLOFF 时间内

	// 清空redis，重新添加状态
	redisConn.FlushAll(ctx)
	//_ = AddToTimelineItem(mockTimeline, mockStatus1, true)
	_ = AddToTimelineItem(mockTimeline, mockStatus2, true)
	firstRebolg := &models.Status{Id: 8, ReblogId: &mockStatus1.Id}
	_ = AddToTimelineItem(mockTimeline, firstRebolg, true)
	extraRebolg1 := &models.Status{Id: 9, ReblogId: &mockStatus1.Id}
	_ = AddToTimelineItem(mockTimeline, extraRebolg1, true)
	extraRebolg2 := &models.Status{Id: 10, ReblogId: &mockStatus1.Id}
	_ = AddToTimelineItem(mockTimeline, extraRebolg2, true)

	// 检查时间线内容，应该只有 1 2 8
	timeline, err = redisConn.ZRange(ctx, timelineKey, 0, -1).Result()
	assert.Nil(t, err)
	assert.EqualValuesf(t, []string{utils.ToString(mockStatus2.Id), utils.ToString(firstRebolg.Id)}, timeline, "timeline should be %v", timeline)

	// 检查转发集合内容（feed:home:1:reblogs），应该只有 8 -> 1
	reblogs, err = redisConn.ZRangeWithScores(ctx, reblogsKey, 0, -1).Result()
	assert.Nil(t, err)
	assert.Len(t, reblogs, 1)
	assert.EqualValuesf(t, utils.ToString(firstRebolg.Id), utils.ToString(reblogs[0].Score), "reblogs should be %v", reblogs)
	assert.EqualValuesf(t, utils.ToString(*firstRebolg.ReblogId), reblogs[0].Member, "reblogs should be %v", reblogs)

	// 检查转发集合内容（feed:home:1:reblogs:{reblogId}），应该只有 9，10
	extraReblogsKey := fmt.Sprintf("feed:%s:%s:reblogs:%s", mockTimeline.Type, timelineId, utils.ToString(mockStatus1.Id))
	extraReblogs, err := redisConn.SMembers(ctx, extraReblogsKey).Result()
	assert.Nil(t, err)
	assert.Len(t, extraReblogs, 2)
	assert.EqualValuesf(t, []string{utils.ToString(extraRebolg1.Id), utils.ToString(extraRebolg2.Id)}, extraReblogs, "extraReblogs should be %v", extraReblogs)
}

func TestRemoveTimelineItem(t *testing.T) {
	//defer clearRedis()
	redisConn := db.GetRedis()
	ctx := context.Background()

	_ = AddToTimelineItem(mockTimeline, mockStatus2, true)
	firstRebolg := &models.Status{Id: 8, ReblogId: &mockStatus1.Id}
	_ = AddToTimelineItem(mockTimeline, firstRebolg, true)
	extraRebolg1 := &models.Status{Id: 9, ReblogId: &mockStatus1.Id}
	_ = AddToTimelineItem(mockTimeline, extraRebolg1, true)
	extraRebolg2 := &models.Status{Id: 10, ReblogId: &mockStatus1.Id}
	_ = AddToTimelineItem(mockTimeline, extraRebolg2, true)

	// 测试多次转发被删除的清空
	err := RemoveTimelineItem(mockTimeline, extraRebolg2, true)
	assert.Nil(t, err)
	// 这时候时间线应该只有 2 8
	timelineKey := fmt.Sprintf(mockTimeline.GetKeyFormat(), mockTimeline.GetTimelineId())
	timeline, err := redisConn.ZRange(ctx, timelineKey, 0, -1).Result()
	assert.Nil(t, err)
	assert.EqualValuesf(t, []string{utils.ToString(mockStatus2.Id), utils.ToString(firstRebolg.Id)}, timeline, "timeline should be %v", timeline)

	// 检查转发集合内容（feed:home:1:reblogs），应该只有 8 -> 1
	reblogsKey := timelineKey + ":reblogs"
	reblogs, err := redisConn.ZRangeWithScores(ctx, reblogsKey, 0, -1).Result()
	assert.Nil(t, err)
	assert.Len(t, reblogs, 1)
	assert.EqualValuesf(t, utils.ToString(firstRebolg.Id), utils.ToString(reblogs[0].Score), "reblogs should be %v", reblogs)
	assert.EqualValuesf(t, utils.ToString(*firstRebolg.ReblogId), reblogs[0].Member, "reblogs should be %v", reblogs)

	// 转发集合内容（feed:home:1:reblogs:{reblogId}）， 还是有 9 10，因为 9和10 是多次转发的，并且不在主时间线上
	extraReblogsKey := reblogsKey + ":" + utils.ToString(mockStatus1.Id)
	extraReblogs, err := redisConn.SMembers(ctx, extraReblogsKey).Result()
	assert.Nil(t, err)
	assert.Len(t, extraReblogs, 2)
	assert.EqualValuesf(t, []string{utils.ToString(extraRebolg1.Id), utils.ToString(extraRebolg2.Id)}, extraReblogs, "extraReblogs should be %v", extraReblogs)

	// 删除 在时间线上的转发 8
	err = RemoveTimelineItem(mockTimeline, firstRebolg, true)
	assert.Nil(t, err)

	// 这时候 reblogs 应该 8被替换成 9
	reblogs, err = redisConn.ZRangeWithScores(ctx, reblogsKey, 0, -1).Result()
	assert.Nil(t, err)
	assert.Len(t, reblogs, 1)
	assert.EqualValuesf(t, utils.ToString(extraRebolg1.Id), utils.ToString(reblogs[0].Score), "reblogs should be %v", reblogs)

	// 主时间线上 有 2 9
	timeline, err = redisConn.ZRange(ctx, timelineKey, 0, -1).Result()
	assert.Nil(t, err)
	assert.EqualValuesf(t, []string{utils.ToString(mockStatus2.Id), utils.ToString(extraRebolg1.Id)}, timeline, "timeline should be %v", timeline)

}

func TestTrimTimeName(t *testing.T) {
	defer clearRedis()

	// 添加一些状态到时间线，使其长度超过 MAX_ITEMS
	for i := 0; i <= MAX_ITEMS+100; i++ {
		id := uint64(i + 100)
		var status *models.Status
		// id 能被 3 整除的状态，是转发状态
		if id%3 == 0 {
			status = &models.Status{Id: id, ReblogId: &mockStatus1.Id}
		} else {
			status = &models.Status{Id: id}
		}

		_ = AddToTimelineItem(mockTimeline, status, true)
	}

	// 测试修剪时间线
	err := TrimTimeline(mockTimeline)
	if err != nil {
		t.Errorf("Failed to trim timeline: %v", err)
	}

	redisConn := db.GetRedis()
	ctx := context.Background()
	timelineKey := fmt.Sprintf(mockTimeline.GetKeyFormat(), mockTimeline.GetTimelineId())
	// 时间线上应该只有 MAX_ITEMS 个状态

	timeline, err := redisConn.ZRange(ctx, timelineKey, 0, -1).Result()
	assert.Nil(t, err)
	assert.Len(t, timeline, MAX_ITEMS)

}

func TestCleanFeeds(t *testing.T) {
	//defer clearRedis()
	mockTimeline1 := &MockTimeline{Id: 1, Type: "home"}
	mockTimeline2 := &MockTimeline{Id: 2, Type: "home"}

	// 随机添加一些状态到时间线
	for i := 0; i <= 100; i++ {
		id := uint64(i + 100)
		var status *models.Status
		// id 能被 3 整除的状态，是转发状态
		if id%3 == 0 {
			status = &models.Status{Id: id, ReblogId: &mockStatus1.Id}
		} else {
			status = &models.Status{Id: id}
		}

		_ = AddToTimelineItem(mockTimeline1, status, true)
		_ = AddToTimelineItem(mockTimeline2, status, true)
	}

	// 测试清除多个时间线
	err := CleanFeeds(mockTimeline1, []string{"1", "2"})
	if err != nil {
		t.Errorf("Failed to clean feeds: %v", err)
	}

	// 这时候 redis 应该没有 feed:home:1 和 feed:home:2和相关的key
	redisConn := db.GetRedis()
	ctx := context.Background()

	// 检查有没有 feed:home: 开头的key
	keys, err := redisConn.Keys(ctx, "feed:home:*").Result()
	assert.Nil(t, err)
	assert.Len(t, keys, 0)
}

func clearRedis() {
	// 清空redis
	ctx := context.Background()
	redisConn := db.GetRedis()
	_ = redisConn.FlushAll(ctx).Err()
	return
}
