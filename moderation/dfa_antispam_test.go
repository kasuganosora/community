package moderation

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redisdb "github.com/kasuganosora/community/db/redis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestDFATrieAndGradedFilterWithWhitelist(t *testing.T) {
	filter := NewGradedFilter([]GradedRule{
		{Word: "赌博网站", Level: FilterLevelBlock},
		{Word: "代开发票", Level: FilterLevelReview, Areas: []string{"status", "reply"}},
		{Word: "剧透", Level: FilterLevelMask, Areas: []string{"reply"}},
	}, []string{"合法发票报销"})

	// Obfuscated full-width & zero-width split characters still trigger block.
	res := filter.Evaluate("status", "欢迎访问 赌\u200b博.网_站！")
	require.Equal(t, FilterLevelBlock, res.Level)
	require.Contains(t, res.Matched, "赌博网站")

	// Whitelisted phrase is exempted.
	resWhite := filter.Evaluate("status", "公司提供合法发票报销流程")
	require.Equal(t, FilterLevelAllow, resWhite.Level)

	// Area-scoped mask applies only in "reply", not "dm".
	resDM := filter.Evaluate("dm", "这里有剧透内容")
	require.Equal(t, FilterLevelAllow, resDM.Level)

	resReply := filter.Evaluate("reply", "小心这里有剧透哦")
	require.Equal(t, FilterLevelMask, resReply.Level)
	require.Equal(t, "小心这里有**哦", resReply.MaskedText)

	// Preserves original casing, masks obfuscated spans, and never masks whitelisted words.
	enFilter := NewGradedFilter([]GradedRule{
		{Word: "ass", Level: FilterLevelMask},
		{Word: "微信", Level: FilterLevelMask},
	}, []string{"class"})
	resMixed := enFilter.Evaluate("status", "My Class is great, no b.a.d a-s-s or 微.信 spam!")
	require.Equal(t, FilterLevelMask, resMixed.Level)
	require.Equal(t, "My Class is great, no b.a.d ***** or *** spam!", resMixed.MaskedText)
}

func TestAntiSpamGuardMemoryAndRedis(t *testing.T) {
	cfg := AntiSpamConfig{
		Enabled:           true,
		DuplicateWindow:   time.Minute,
		DuplicateMaxCount: 2,
		ReplyFloodWindow:  time.Minute,
		ReplyFloodMax:     3,
	}

	t.Run("memory fallback catches obfuscated duplicates and reply floods", func(t *testing.T) {
		redisdb.SetUniversalRedis(nil)
		g := NewAntiSpamGuard(cfg)
		ctx := context.Background()

		require.NoError(t, g.CheckStatus(ctx, 10, 0, "Buy cheap followers now!!!"))
		require.NoError(t, g.CheckStatus(ctx, 10, 0, "buy　cheap\u200bfollowers now"))
		require.ErrorIs(t, g.CheckStatus(ctx, 10, 0, "BUY CHEAP FOLLOWERS NOW"), ErrSpamDuplicate)

		// Distinct replies to same parent hit ReplyFloodMax=3 on the 4th post.
		require.NoError(t, g.CheckStatus(ctx, 20, 99, "first unique reply body"))
		require.NoError(t, g.CheckStatus(ctx, 20, 99, "second unique reply body"))
		require.NoError(t, g.CheckStatus(ctx, 20, 99, "third unique reply body"))
		require.ErrorIs(t, g.CheckStatus(ctx, 20, 99, "fourth unique reply body"), ErrSpamFlood)
	})

	t.Run("redis backed anti-spam", func(t *testing.T) {
		mr := miniredis.RunT(t)
		client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		t.Cleanup(func() {
			redisdb.SetUniversalRedis(nil)
			_ = client.Close()
		})
		redisdb.SetUniversalRedis(client)

		g := NewAntiSpamGuard(cfg)
		ctx := context.Background()
		require.NoError(t, g.CheckStatus(ctx, 30, 0, "repeated advertisement text"))
		require.NoError(t, g.CheckStatus(ctx, 30, 0, "repeated advertisement text"))
		require.ErrorIs(t, g.CheckStatus(ctx, 30, 0, "repeated advertisement text"), ErrSpamDuplicate)
	})
}
