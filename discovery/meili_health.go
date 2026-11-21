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

type meiliDiskTelemetry struct {
	Policy              string `json:"policy"`
	DatabaseBytes       int64  `json:"database_bytes,omitempty"`
	DatabaseBudgetBytes int64  `json:"database_budget_bytes,omitempty"`
	DatabaseSizeSource  string `json:"database_size_source,omitempty"`
}

func validMeiliSizeSource(source string) bool {
	return source == "" || source == "owned_indexes" || source == "used_database" || source == "database"
}

// Health is deployment-wide, and an expired worker must not overwrite a new
// owner's error or disk sample.
var meiliOperationScript = redis.NewScript(`
if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end
redis.call('HSET',KEYS[2],ARGV[2],ARGV[3])
redis.call('EXPIRE',KEYS[2],3600)
return 1`)

func (p *MeiliProvider) recordOperation(token, stage string, operationErr error) {
	if stage != "build" && stage != "query" {
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
	p.writeHealth(token, stage, entry)
}

func (p *MeiliProvider) writeHealth(token, field string, entry any) {
	client := p.redis()
	if client == nil {
		return
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, state := p.keys()
	_ = meiliOperationScript.Run(ctx, client, []string{lease, state + ":health"}, token, field, string(data)).Err()
}

func (p *MeiliProvider) readHealth(ctx context.Context, status *MeiliStatus) {
	_, state := p.keys()
	values, err := p.redis().HMGet(ctx, state+":health", "disk", "build", "query").Result()
	if err != nil {
		return
	}
	// Before disk samples had a separate field, workers attached their last
	// measurement to build health. A new disk sample always supersedes it.
	legacy := values[0] == nil
	if legacy {
		values[0] = values[1]
	}
	for i, value := range values {
		encoded, ok := value.(string)
		if !ok || len(encoded) > 512 {
			continue
		}
		if i == 0 {
			// A fenced /stats probe is newer than the local or manifest fallback.
			// Keep its zero values too, so unreadable sizes do not look current.
			var disk meiliDiskTelemetry
			if json.Unmarshal([]byte(encoded), &disk) == nil && disk.Policy == p.PolicyVersion() && validMeiliSizeSource(disk.DatabaseSizeSource) && (!legacy || disk.DatabaseBudgetBytes > 0) {
				status.DatabaseBytes = disk.DatabaseBytes
				status.DatabaseBudgetBytes = disk.DatabaseBudgetBytes
				status.DatabaseSizeSource = disk.DatabaseSizeSource
			}
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
		if i == 1 {
			status.LastBuildError, status.LastBuildErrorAt = entry.Code, &entry.At
		} else {
			status.LastQueryError, status.LastQueryErrorAt = entry.Code, &entry.At
		}
	}
}
