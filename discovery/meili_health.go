package discovery

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

type meiliOperationError struct {
	Policy string    `json:"policy"`
	Code   string    `json:"code"`
	At     time.Time `json:"at"`
}

// Health is deployment-wide, and an expired worker must not clear a new
// owner's failure. Only fixed codes and timestamps enter this record.
var meiliOperationScript = redis.NewScript(`
if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end
redis.call('HSET',KEYS[2],ARGV[2],ARGV[3])
redis.call('EXPIRE',KEYS[2],3600)
return 1`)

func (p *MeiliProvider) recordOperation(token, stage string, operationErr error) {
	if p.redis() == nil || (stage != "build" && stage != "query") {
		return
	}
	entry := meiliOperationError{Policy: p.PolicyVersion()}
	if operationErr != nil {
		entry.Code = "query_unavailable"
		if stage == "build" {
			entry.Code = meiliSafeBudgetReason(operationErr)
		}
		entry.At = time.Now().UTC()
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, state := p.keys()
	_ = meiliOperationScript.Run(ctx, p.redis(), []string{lease, state + ":health"}, token, stage, string(data)).Err()
}

func (p *MeiliProvider) operationErrors(ctx context.Context, status *MeiliStatus) {
	_, state := p.keys()
	values, err := p.redis().HMGet(ctx, state+":health", "build", "query").Result()
	if err != nil {
		return
	}
	for i, value := range values {
		encoded, ok := value.(string)
		if !ok || len(encoded) > 512 {
			continue
		}
		var entry meiliOperationError
		if json.Unmarshal([]byte(encoded), &entry) != nil || entry.Policy != p.PolicyVersion() || entry.Code == "" || entry.At.IsZero() {
			continue
		}
		// Never render arbitrary Redis text as an operational error message.
		if entry.Code != "query_unavailable" && entry.Code != "build_unavailable" && entry.Code != "resource_budget_exceeded" {
			continue
		}
		if i == 0 {
			status.LastBuildError, status.LastBuildErrorAt = entry.Code, &entry.At
		} else {
			status.LastQueryError, status.LastQueryErrorAt = entry.Code, &entry.At
		}
	}
}
