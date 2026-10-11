package feed

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// scriptCall computes arguments at enqueue time. In particular, retries must
// subtract elapsed time from a SQL lease, not grant its original duration again.
type scriptCall struct {
	keys []string
	args func() ([]interface{}, error)
}

// pipelineScript retries only explicit NOSCRIPT replies, never ambiguous
// transport failures. Both rounds use the caller's original deadline and key
// routing, including after Cluster failover or slot migration.
func (s *FeedService) pipelineScript(ctx context.Context, source, hash string, calls []*scriptCall) ([]*redis.Cmd, error) {
	commands := make([]*redis.Cmd, len(calls))
	enqueue := func(pipe redis.Pipeliner, index int, fallback bool) {
		args, err := calls[index].args()
		if err != nil {
			commands[index] = redis.NewCmd(ctx)
			commands[index].SetErr(err)
			return
		}
		if fallback {
			commands[index] = pipe.Eval(ctx, source, calls[index].keys, args...)
		} else {
			commands[index] = pipe.EvalSha(ctx, hash, calls[index].keys, args...)
		}
	}
	_, firstErr := s.redis.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for index, call := range calls {
			if call != nil {
				enqueue(pipe, index, false)
			}
		}
		return nil
	})
	if firstErr != nil && !redis.HasErrorPrefix(firstErr, "NOSCRIPT") {
		return commands, firstErr
	}
	missing := []int{}
	for index, cmd := range commands {
		if cmd != nil && redis.HasErrorPrefix(cmd.Err(), "NOSCRIPT") {
			missing = append(missing, index)
		}
	}
	if len(missing) == 0 {
		return commands, firstErr
	}
	if err := ctx.Err(); err != nil {
		return commands, err
	}
	_, retryErr := s.redis.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, index := range missing {
			enqueue(pipe, index, true)
		}
		return nil
	})
	return commands, retryErr
}
