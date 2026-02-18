package feed

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

// newTestRedis creates a Redis client for testing
func newTestRedis(t *testing.T) *redis.Client {
	config := testutil.GetTestConfig()

	client := redis.NewClient(&redis.Options{
		Addr:         testutil.GetRedisAddr(),
		Password:     testutil.GetRedisPassword(),
		DB:           config.RedisDB,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis not available, skipping test: %v", err)
		return nil
	}

	// Flush test database
	client.FlushDB(ctx)

	return client
}

// MockTimeline implements Timeline interface for testing
type MockTimeline struct {
	KeyFormat  string
	TimelineId string
}

func (m *MockTimeline) GetKeyFormat() string {
	return m.KeyFormat
}

func (m *MockTimeline) GetTimelineId() string {
	return m.TimelineId
}

func (m *MockTimeline) Rebuild() error {
	return nil
}

func TestListParam_SetMaxId(t *testing.T) {
	p := &ListParam{}
	p.SetMaxId(123)
	assert.Equal(t, uint64(123), p.MaxId)
}

func TestListParam_SetMinId(t *testing.T) {
	p := &ListParam{}
	p.SetMinId(456)
	assert.Equal(t, uint64(456), p.MinId)
}

func TestListParam_SetSinceId(t *testing.T) {
	p := &ListParam{}
	p.SetSinceId(789)
	assert.Equal(t, uint64(789), p.SinceId)
}

func TestListParam_SetLimit(t *testing.T) {
	p := &ListParam{}
	p.SetLimit(20)
	assert.Equal(t, uint64(20), p.Limit)
}

func TestListParam_GetRedisRangeMinMax(t *testing.T) {
	t.Run("With MaxId and MinId", func(t *testing.T) {
		p := &ListParam{MaxId: 100, MinId: 50}
		start, end := p.GetRedisRangeMinMax()
		assert.Equal(t, "50", start)
		assert.Equal(t, "100", end)
	})

	t.Run("Without MaxId and MinId", func(t *testing.T) {
		p := &ListParam{}
		start, end := p.GetRedisRangeMinMax()
		assert.Equal(t, "-inf", start)
		assert.Equal(t, "+inf", end)
	})

	t.Run("With only MaxId", func(t *testing.T) {
		p := &ListParam{MaxId: 100}
		start, end := p.GetRedisRangeMinMax()
		assert.Equal(t, "-inf", start)
		assert.Equal(t, "100", end)
	})

	t.Run("With only MinId", func(t *testing.T) {
		p := &ListParam{MinId: 50}
		start, end := p.GetRedisRangeMinMax()
		assert.Equal(t, "50", start)
		assert.Equal(t, "+inf", end)
	})
}

func TestListParam_GetRedisRangeOffsetCount(t *testing.T) {
	t.Run("With SinceId and Limit", func(t *testing.T) {
		p := &ListParam{SinceId: 10, Limit: 20}
		offset, count := p.GetRedisRangeOffsetCount()
		assert.Equal(t, int64(10), offset)
		assert.Equal(t, int64(30), count)
	})

	t.Run("Without SinceId and Limit", func(t *testing.T) {
		p := &ListParam{}
		offset, count := p.GetRedisRangeOffsetCount()
		assert.Equal(t, int64(0), offset)
		assert.Equal(t, int64(-1), count)
	})
}

func TestRedisTimelineOperations(t *testing.T) {
	client := newTestRedis(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	t.Run("Add regular status to timeline", func(t *testing.T) {
		status := &models.Status{Id: 1001}
		timelineKey := "test:feed:user1"

		err := client.ZAdd(ctx, timelineKey, redis.Z{Score: float64(status.Id), Member: strconv.FormatUint(status.Id, 10)}).Err()
		assert.NoError(t, err)

		result, err := client.ZRange(ctx, timelineKey, 0, -1).Result()
		assert.NoError(t, err)
		assert.Contains(t, result, "1001")
	})

	t.Run("Add reblog status to timeline", func(t *testing.T) {
		reblogId := uint64(2001)
		status := &models.Status{Id: 2002, ReblogId: &reblogId}

		timelineKey := "test:feed:user2"
		reblogKey := timelineKey + ":reblogs"

		err := client.ZAdd(ctx, timelineKey, redis.Z{Score: float64(status.Id), Member: strconv.FormatUint(status.Id, 10)}).Err()
		assert.NoError(t, err)

		err = client.ZAdd(ctx, reblogKey, redis.Z{Score: float64(status.Id), Member: strconv.FormatUint(reblogId, 10)}).Err()
		assert.NoError(t, err)

		result, err := client.ZRange(ctx, timelineKey, 0, -1).Result()
		assert.NoError(t, err)
		assert.Contains(t, result, "2002")
	})

	t.Run("Remove status from timeline", func(t *testing.T) {
		timelineKey := "test:feed:user3"

		client.ZAdd(ctx, timelineKey, redis.Z{Score: 3001, Member: "3001"})
		client.ZAdd(ctx, timelineKey, redis.Z{Score: 3002, Member: "3002"})

		err := client.ZRem(ctx, timelineKey, "3001").Err()
		assert.NoError(t, err)

		result, err := client.ZRange(ctx, timelineKey, 0, -1).Result()
		assert.NoError(t, err)
		assert.NotContains(t, result, "3001")
		assert.Contains(t, result, "3002")
	})

	t.Run("Trim timeline", func(t *testing.T) {
		timelineKey := "test:feed:user4"

		for i := 0; i < 600; i++ {
			client.ZAdd(ctx, timelineKey, redis.Z{Score: float64(i), Member: strconv.Itoa(i)})
		}

		err := client.ZRemRangeByRank(ctx, timelineKey, 0, -(MAX_ITEMS + 1)).Err()
		assert.NoError(t, err)

		count, err := client.ZCard(ctx, timelineKey).Result()
		assert.NoError(t, err)
		assert.LessOrEqual(t, count, int64(MAX_ITEMS))
	})
}

func TestPubSub(t *testing.T) {
	client := newTestRedis(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	t.Run("Publish and subscribe message", func(t *testing.T) {
		channel := "test:channel:pubsub"

		pubsub := client.Subscribe(ctx, channel)
		defer pubsub.Close()

		_, err := pubsub.Receive(ctx)
		assert.NoError(t, err)

		msg := map[string]any{
			"type":    "update",
			"payload": "test payload",
		}
		msgJson, _ := json.Marshal(msg)

		err = client.Publish(ctx, channel, msgJson).Err()
		assert.NoError(t, err)

		msgCh := pubsub.Channel()
		select {
		case received := <-msgCh:
			assert.Equal(t, channel, received.Channel)
		case <-time.After(time.Second):
			t.Error("Did not receive message")
		}
	})
}

func TestGetRedisChannelName(t *testing.T) {
	timeline := &MockTimeline{KeyFormat: "feed:home:%s", TimelineId: "123"}
	result := GetRedisChannelName(timeline)
	assert.Equal(t, "subscribed:feed:home:123", result)
}

func TestCleanFeeds(t *testing.T) {
	client := newTestRedis(t)
	if client == nil {
		return
	}
	defer client.Close()

	ctx := context.Background()

	t.Run("Clean multiple feeds", func(t *testing.T) {
		feeds := []string{"user1", "user2", "user3"}
		for _, feedId := range feeds {
			timelineKey := "test:feed:" + feedId
			reblogKey := timelineKey + ":reblogs"

			client.ZAdd(ctx, timelineKey, redis.Z{Score: 1, Member: "1"})
			client.ZAdd(ctx, reblogKey, redis.Z{Score: 1, Member: "r1"})
		}

		for _, feedId := range feeds {
			timelineKey := "test:feed:" + feedId
			reblogKey := timelineKey + ":reblogs"
			client.Del(ctx, timelineKey, reblogKey)
		}

		for _, feedId := range feeds {
			timelineKey := "test:feed:" + feedId
			exists := client.Exists(ctx, timelineKey).Val()
			assert.Equal(t, int64(0), exists)
		}
	})
}

func TestErrFeedRegenerating(t *testing.T) {
	assert.EqualError(t, ErrFeedRegenerating, "feed is regenerating")
}

func TestConstants(t *testing.T) {
	assert.Equal(t, 512, MAX_ITEMS)
	assert.Equal(t, 40, REBLOG_FALLOFF)
	assert.Equal(t, 7, MAX_AGE_DAYS)
}
