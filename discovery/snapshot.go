package discovery

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	appredis "github.com/kasuganosora/community/db/redis"
	"github.com/redis/go-redis/v9"
)

const snapshotMaximumBytes = 48 << 10

func feedRedis() redis.UniversalClient {
	if !appredis.HasUniversalRedis() && len(appredis.GetRedises()) == 0 {
		return nil
	}
	return appredis.GetUniversalRedis()
}

func feedGeneration() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

type feedCursor struct {
	UserID     uint64 `json:"u"`
	Revision   uint64 `json:"r"`
	Generation string `json:"g"`
	Algorithm  string `json:"a"`
	Block      int    `json:"b"`
	Offset     int    `json:"o"`
	Expires    int64  `json:"e"`
}

func encodeFeedCursor(cursor feedCursor) (string, error) {
	keys := CursorSigningKeys()
	if len(keys) == 0 {
		return "", ErrUnavailable
	}
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, keys[0])
	_, _ = mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeFeedCursor(token string, accountID, revision uint64, now time.Time) (feedCursor, error) {
	var cursor feedCursor
	if len(token) > 2048 {
		return cursor, ErrResetRequired
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return cursor, ErrResetRequired
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursor, ErrResetRequired
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursor, ErrResetRequired
	}
	valid := false
	for _, key := range CursorSigningKeys() {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write(data)
		if hmac.Equal(signature, mac.Sum(nil)) {
			valid = true
			break
		}
	}
	if !valid || json.Unmarshal(data, &cursor) != nil || cursor.UserID != accountID || cursor.Revision != revision || cursor.Algorithm != AlgorithmVersion || len(cursor.Generation) != 32 || cursor.Expires <= now.Unix() || cursor.Block < 0 || cursor.Block >= 10 || cursor.Offset < 0 || cursor.Offset >= BlockSize {
		return cursor, ErrResetRequired
	}
	return cursor, nil
}

// All user keys share a hash tag. Whole snapshots live in a hash to keep Lua
// keys explicit and ensure the two-per-user cap is atomic on Redis Cluster.
func snapshotKeys(accountID uint64) (string, string) {
	prefix := "community:discovery:{user:" + strconv.FormatUint(accountID, 10) + "}:"
	return prefix + "snapshots", prefix + "expiry"
}

var reserveSnapshotScript = redis.NewScript(`
redis.call('ZREMRANGEBYSCORE',KEYS[1],'-inf',ARGV[1])
if redis.call('ZCARD',KEYS[1])>=tonumber(ARGV[3]) then return 0 end
redis.call('ZADD',KEYS[1],ARGV[2],ARGV[4])
redis.call('PEXPIRE',KEYS[1],ARGV[5])
return 1`)

var publishSnapshotScript = redis.NewScript(`
local expired=redis.call('ZRANGEBYSCORE',KEYS[2],'-inf',ARGV[1])
for _,generation in ipairs(expired) do redis.call('HDEL',KEYS[1],generation) end
redis.call('ZREMRANGEBYSCORE',KEYS[2],'-inf',ARGV[1])
local latest=redis.call('ZREVRANGE',KEYS[2],0,0)
if #latest>0 then
 local raw=redis.call('HGET',KEYS[1],latest[1])
 if raw then
  local ok,old=pcall(cjson.decode,raw)
  if ok and old.revision==tonumber(ARGV[5]) and old.preference==ARGV[6] and old.mode==ARGV[7] and old.policy_version==ARGV[9] and tonumber(redis.call('ZSCORE',KEYS[2],latest[1]))>tonumber(ARGV[2])-5000 then return cjson.encode({snapshot=raw,evicted=''}) end
 end
end
redis.call('HSET',KEYS[1],ARGV[3],ARGV[4])
redis.call('PEXPIRE',KEYS[1],ARGV[8])
redis.call('ZADD',KEYS[2],ARGV[2],ARGV[3])
local evicted=''
while redis.call('ZCARD',KEYS[2])>2 do
 local old=redis.call('ZRANGE',KEYS[2],0,0)
 redis.call('ZREM',KEYS[2],old[1]);redis.call('HDEL',KEYS[1],old[1])
 evicted=old[1]
end
redis.call('PEXPIRE',KEYS[1],ARGV[8]);redis.call('PEXPIRE',KEYS[2],ARGV[8])
return cjson.encode({snapshot=ARGV[4],evicted=evicted})`)

func saveSnapshot(ctx context.Context, snapshot Snapshot) (Snapshot, error) {
	client := feedRedis()
	if client == nil {
		return Snapshot{}, ErrUnavailable
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return Snapshot{}, err
	}
	if len(data) > snapshotMaximumBytes {
		return Snapshot{}, ErrUnavailable
	}
	config := feedConfig()
	limit := config.MaxActiveSnapshots
	// Reserve conservatively for Redis object overhead, two generations and
	// candidate pools. Redis noeviction errors leave existing snapshots intact.
	budgetLimit := int((config.MaxCacheBytes - (8 << 20)) / (64 << 10))
	if budgetLimit < limit {
		limit = budgetLimit
	}
	if limit <= 0 {
		return Snapshot{}, ErrUnavailable
	}
	const permits = "community:discovery:{snapshot-budget}:permits"
	permit := strconv.FormatUint(snapshot.UserID, 10) + ":" + snapshot.Generation
	reserved, err := reserveSnapshotScript.Run(ctx, client, []string{permits}, time.Now().UnixMilli(), snapshot.ExpiresAt.UnixMilli(), limit, permit, SnapshotTTL.Milliseconds()).Int()
	if err != nil || reserved != 1 {
		return Snapshot{}, ErrUnavailable
	}
	dataKey, expiryKey := snapshotKeys(snapshot.UserID)
	published, err := publishSnapshotScript.Run(ctx, client, []string{dataKey, expiryKey}, time.Now().UnixMilli(), snapshot.ExpiresAt.UnixMilli(), snapshot.Generation, string(data), snapshot.Revision, snapshot.Preference, snapshot.Mode, SnapshotTTL.Milliseconds(), snapshot.PolicyVersion).Text()
	if err != nil {
		_ = client.ZRem(ctx, permits, permit).Err()
		return Snapshot{}, ErrUnavailable
	}
	var publication struct {
		Snapshot string `json:"snapshot"`
		Evicted  string `json:"evicted"`
	}
	var result Snapshot
	if json.Unmarshal([]byte(published), &publication) != nil || json.Unmarshal([]byte(publication.Snapshot), &result) != nil {
		return Snapshot{}, ErrUnavailable
	}
	if result.Generation != snapshot.Generation {
		_ = client.ZRem(ctx, permits, permit).Err()
	}
	if publication.Evicted != "" {
		_ = client.ZRem(ctx, permits, strconv.FormatUint(snapshot.UserID, 10)+":"+publication.Evicted).Err()
	}
	return result, nil
}

func loadSnapshot(ctx context.Context, cursor feedCursor, now time.Time) (Snapshot, error) {
	var snapshot Snapshot
	client := feedRedis()
	if client == nil {
		return snapshot, ErrResetRequired
	}
	dataKey, _ := snapshotKeys(cursor.UserID)
	data, err := client.HGet(ctx, dataKey, cursor.Generation).Bytes()
	if err != nil || len(data) > snapshotMaximumBytes || json.Unmarshal(data, &snapshot) != nil {
		return snapshot, ErrResetRequired
	}
	if snapshot.UserID != cursor.UserID || snapshot.Revision != cursor.Revision || snapshot.Algorithm != AlgorithmVersion || snapshot.Generation != cursor.Generation || !now.Before(snapshot.ExpiresAt) || snapshot.ExpiresAt.Unix() != cursor.Expires {
		return snapshot, ErrResetRequired
	}
	if len(snapshot.Blocks) > 10 || cursor.Block >= len(snapshot.Blocks) || cursor.Offset >= len(snapshot.Blocks[cursor.Block]) {
		return snapshot, ErrResetRequired
	}
	for _, block := range snapshot.Blocks {
		if len(block) > BlockSize {
			return snapshot, ErrResetRequired
		}
	}
	return snapshot, nil
}

func nextFeedCursor(snapshot Snapshot, block, offset int) (*string, error) {
	for block < len(snapshot.Blocks) && offset >= len(snapshot.Blocks[block]) {
		block++
		offset = 0
	}
	if block >= len(snapshot.Blocks) {
		return nil, nil
	}
	token, err := encodeFeedCursor(feedCursor{UserID: snapshot.UserID, Revision: snapshot.Revision, Generation: snapshot.Generation, Algorithm: AlgorithmVersion, Block: block, Offset: offset, Expires: snapshot.ExpiresAt.Unix()})
	if err != nil {
		return nil, fmt.Errorf("sign recommendation cursor: %w", err)
	}
	return &token, nil
}
