package feed

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type scriptedPipeline struct {
	RedisClient
	run func(context.Context, []redis.Cmder) error
}

// Only the commands exercised by pipelineScript are implemented. No Client
// is constructed: recent go-redis versions may initialize network features
// before the first explicit command, which would corrupt lease timing tests.
type scriptCommandCollector struct {
	redis.Pipeliner
	commands []redis.Cmder
}

func (p *scriptCommandCollector) collect(ctx context.Context, name, script string, keys []string, args ...interface{}) *redis.Cmd {
	values := []interface{}{name, script, len(keys)}
	for _, key := range keys {
		values = append(values, key)
	}
	values = append(values, args...)
	command := redis.NewCmd(ctx, values...)
	p.commands = append(p.commands, command)
	return command
}
func (p *scriptCommandCollector) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	return p.collect(ctx, "eval", script, keys, args...)
}
func (p *scriptCommandCollector) EvalSha(ctx context.Context, hash string, keys []string, args ...interface{}) *redis.Cmd {
	return p.collect(ctx, "evalsha", hash, keys, args...)
}
func (p *scriptedPipeline) Pipelined(ctx context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	pipe := &scriptCommandCollector{}
	if err := fn(pipe); err != nil {
		return nil, err
	}
	return pipe.commands, p.run(ctx, pipe.commands)
}

func TestScriptPipelineMixedSuccessRetriesOnlyMissing(t *testing.T) {
	rounds := 0
	service := NewFeedService(&scriptedPipeline{run: func(_ context.Context, cmds []redis.Cmder) error {
		rounds++
		if rounds == 1 {
			require.Len(t, cmds, 2)
			require.Equal(t, "evalsha", cmds[0].Name())
			cmds[0].(*redis.Cmd).SetVal(int64(1))
			cmds[1].SetErr(redis.ErrNoScript)
			return redis.ErrNoScript
		}
		require.Equal(t, 2, rounds)
		require.Len(t, cmds, 1, "successful slot must not be replayed")
		require.Equal(t, "eval", cmds[0].Name())
		require.Equal(t, "second", cmds[0].Args()[3])
		cmds[0].(*redis.Cmd).SetVal(int64(1))
		return nil
	}})
	args := func() ([]interface{}, error) { return nil, nil }
	cmds, err := service.pipelineScript(context.Background(), "return 1", redis.NewScript("return 1").Hash(), []*scriptCall{{keys: []string{"first"}, args: args}, {keys: []string{"second"}, args: args}})
	require.NoError(t, err)
	require.Equal(t, 2, rounds)
	for _, cmd := range cmds {
		require.NoError(t, cmd.Err())
	}
}

func TestScriptPipelineDoesNotRetryAmbiguousTransportFailure(t *testing.T) {
	rounds := 0
	lost := errors.New("lost reply")
	service := NewFeedService(&scriptedPipeline{run: func(_ context.Context, cmds []redis.Cmder) error {
		rounds++
		cmds[0].SetErr(redis.ErrNoScript)
		return lost
	}})
	_, err := service.pipelineScript(context.Background(), "return 1", "sha", []*scriptCall{{keys: []string{"key"}, args: func() ([]interface{}, error) { return nil, nil }}})
	require.ErrorIs(t, err, lost)
	require.Equal(t, 1, rounds)
}

func TestProjectionNOSCRIPTDoesNotRenewElapsedLease(t *testing.T) {
	rounds := 0
	var first int64
	service := NewFeedService(&scriptedPipeline{run: func(ctx context.Context, cmds []redis.Cmder) error {
		rounds++
		require.Len(t, cmds, 1)
		remaining := cmds[0].Args()[8].(int64)
		if rounds == 1 {
			first = remaining
			cmds[0].SetErr(redis.ErrNoScript)
			timer := time.NewTimer(25 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				return ctx.Err()
			}
			return redis.ErrNoScript
		}
		require.Less(t, remaining, first-15, "fallback must subtract elapsed SQL lease budget")
		cmds[0].(*redis.Cmd).SetVal(int64(1))
		return nil
	}})
	timeline := &TestMockTimeline{KeyFormat: "feed:home:%s", TimelineId: "lease"}
	result := service.BatchApplyProjections(context.Background(), []ProjectionTarget{{timeline, Projection{Epoch: "e", BatchID: 1, LeaseGeneration: 1, LeaseToken: "token", RemainingLease: time.Second, StatusID: 1, ObjectVersion: 1}}})
	require.Equal(t, 2, rounds)
	require.NoError(t, result[0])
}

func TestScriptPipelineCancellationPreventsFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rounds := 0
	service := NewFeedService(&scriptedPipeline{run: func(_ context.Context, cmds []redis.Cmder) error {
		rounds++
		cmds[0].SetErr(redis.ErrNoScript)
		cancel()
		return redis.ErrNoScript
	}})
	_, err := service.pipelineScript(ctx, "return 1", "sha", []*scriptCall{{keys: []string{"key"}, args: func() ([]interface{}, error) { return nil, nil }}})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, rounds)
}

func TestHotScriptsRecoverAfterRealScriptFlush(t *testing.T) {
	_, client, service, timeline := protocolFixture(t)
	ctx := context.Background()
	epochs, errs := service.projectionEpochs(ctx, []Timeline{timeline}, true)
	require.NoError(t, errs[0])
	projection := Projection{Epoch: epochs[0], BatchID: 1, LeaseGeneration: 1, LeaseToken: "test", RemainingLease: time.Second, StatusID: 11, ObjectVersion: 1}
	require.NoError(t, service.BatchApplyProjections(ctx, []ProjectionTarget{{timeline, projection}})[0])
	require.NoError(t, client.ScriptFlush(ctx).Err())
	next, errs := service.projectionEpochs(ctx, []Timeline{timeline}, true)
	require.NoError(t, errs[0])
	require.Equal(t, epochs, next)
	projection.Remove = true
	projection.ObjectVersion = 2
	require.NoError(t, service.BatchApplyProjections(ctx, []ProjectionTarget{{timeline, projection}})[0])
	projection.Remove = false
	projection.ObjectVersion = 1
	require.ErrorIs(t, service.BatchApplyProjections(ctx, []ProjectionTarget{{timeline, projection}})[0], ErrSuperseded)
}
