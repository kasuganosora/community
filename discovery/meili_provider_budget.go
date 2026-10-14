package discovery

import (
	"time"

	"context"
	"errors"
	"fmt"
	"github.com/kasuganosora/community/log"
	"net/http"
	"strings"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// Units count a submitted document embedding or a query embedding. They bound
// work rather than claiming a model-independent dollar/token price. Failure
// does not refund units: an external model may already have processed them.
var meiliEmbeddingBudgetScript = redis.NewScript(`
local now=tonumber(redis.call('TIME')[1]); local hour=math.floor(now/3600)
local previous=tonumber(redis.call('HGET',KEYS[1],'hour') or '-1')
local used=tonumber(redis.call('HGET',KEYS[1],'used') or '0')
if previous~=hour then used=0 end
local amount=tonumber(ARGV[1]);local ceiling=tonumber(ARGV[2])
if amount<0 or amount>ceiling or used>ceiling-amount then return 0 end
redis.call('HSET',KEYS[1],'hour',hour,'used',used+amount)
redis.call('EXPIRE',KEYS[1],3700)
return 1`)

func (p *MeiliProvider) reserveEmbedding(ctx context.Context, units int) bool {
	if p.embedding == nil || p.embedding.MaxUnitsPerHour <= 0 || p.redis() == nil {
		return false
	}
	_, key := p.keys()
	result, err := meiliEmbeddingBudgetScript.Run(ctx, p.redis(), []string{key + ":embedding-budget"}, units, p.embedding.MaxUnitsPerHour).Int()
	return err == nil && result == 1
}

// meiliStats is the subset of GET /stats used for the disk budget. Every size
// is optional: v1.12 reports only databaseSize (no usedDatabaseSize and no
// per-index sizes), newer servers add usedDatabaseSize and per-index
// usedIndexSize/indexSize.
type meiliStats struct {
	DatabaseSize     *int64 `json:"databaseSize"`
	UsedDatabaseSize *int64 `json:"usedDatabaseSize"`
	Indexes          map[string]struct {
		IndexSize     *int64 `json:"indexSize"`
		UsedIndexSize *int64 `json:"usedIndexSize"`
	} `json:"indexes"`
}

func positive(v *int64) (int64, bool) {
	if v != nil && *v > 0 {
		return *v, true
	}
	return 0, false
}

// meiliBudgetBytes picks the most specific size the server reports:
// 1. the sum of our own indexes' sizes, when the server reports any;
// 2. usedDatabaseSize, when positive;
// 3. databaseSize, when positive.
// ok is false when no size is readable at all.
func meiliBudgetBytes(stats meiliStats, prefix string) (used int64, source string, ok bool) {
	var own int64
	sized := false
	for uid, index := range stats.Indexes {
		if !strings.HasPrefix(uid, prefix) {
			continue
		}
		if n, has := positive(index.UsedIndexSize); has {
			own, sized = own+n, true
		} else if n, has := positive(index.IndexSize); has {
			own, sized = own+n, true
		}
	}
	if sized {
		return own, "owned_indexes", true
	}
	if n, has := positive(stats.UsedDatabaseSize); has {
		return n, "used_database", true
	}
	if n, has := positive(stats.DatabaseSize); has {
		return n, "database", true
	}
	return 0, "", false
}

func (p *MeiliProvider) checkDiskBudget(ctx context.Context) error {
	ceiling := p.maxDatabaseBytes
	if ceiling <= 0 {
		ceiling = 512 << 20
	}
	var stats meiliStats
	if err := p.http(ctx, http.MethodGet, "/stats", nil, &stats); err != nil {
		return err
	}
	used, source, ok := meiliBudgetBytes(stats, p.connection.Prefix+"discovery_statuses_v1_")
	if !ok {
		// No readable size must not stop the rebuild (and with it all of
		// discovery). The build stays bounded by the document row limit.
		p.warnBudgetUnreadable()
		return nil
	}
	if used > ceiling-(64<<20) {
		return fmt.Errorf("recommendation search disk budget exceeded (%d MiB %s of %d MiB)", used>>20, source, ceiling>>20)
	}
	return nil
}

func (p *MeiliProvider) warnBudgetUnreadable() {
	p.mu.Lock()
	due := time.Since(p.budgetWarnAt) >= time.Hour
	if due {
		p.budgetWarnAt = time.Now()
	}
	p.mu.Unlock()
	if due {
		log.Warn("Discovery Meili /stats reports no usable size; rebuilding without the disk budget (bounded by the document limit)")
	}
}

func meiliByteLength(conn *gorm.DB, column string) string {
	if dialect.IsSQLite(conn) {
		return "LENGTH(CAST(COALESCE(" + column + ", '') AS BLOB))"
	}
	return "OCTET_LENGTH(COALESCE(" + column + ", ''))"
}

var errMeiliPreflightBudget = errors.New("recommendation preload budget exceeded")

// Guard before GORM Preload(Content/Tags), including the bounded quote graph.
// Byte limits cover raw HTML/CW and tag names as well as normalized JSON later.
func meiliPreflight(conn *gorm.DB, roots []uint64) error {
	if len(roots) == 0 {
		return nil
	}
	all := append([]uint64(nil), roots...)
	seen := map[uint64]bool{}
	for _, id := range roots {
		seen[id] = true
	}
	pending := append([]uint64(nil), roots...)
	for depth := 0; depth < 4 && len(pending) > 0; depth++ {
		var refs []struct {
			ID       uint64
			QuoteID  *uint64
			ReblogID *uint64
		}
		if err := conn.Table("statuses").Select("id,quote_id,reblog_id").Where("id IN ?", pending).Find(&refs).Error; err != nil {
			return err
		}
		pending = nil
		for _, ref := range refs {
			for _, id := range []*uint64{ref.QuoteID, ref.ReblogID} {
				if id != nil && *id != 0 && !seen[*id] {
					seen[*id] = true
					pending = append(pending, *id)
					all = append(all, *id)
					if len(all) > 10000 {
						return fmt.Errorf("%w: reference count", errMeiliPreflightBudget)
					}
				}
			}
		}
	}
	type total struct {
		Bytes   int64
		Maximum int64
	}
	var content total
	size := meiliByteLength(conn, "content") + " + " + meiliByteLength(conn, "spoiler_text")
	if err := conn.Table("status_contents").Select("COALESCE(SUM("+size+"),0) AS bytes, COALESCE(MAX("+size+"),0) AS maximum").Where("status_id IN ?", all).Scan(&content).Error; err != nil {
		return err
	}
	if content.Maximum > 65536 || content.Bytes > meiliDocumentBytes {
		return fmt.Errorf("%w: raw content", errMeiliPreflightBudget)
	}
	var tags []struct {
		StatusID uint64
		Count    int64
		Bytes    int64
		Maximum  int64
	}
	tagSize := meiliByteLength(conn, "tags.name") + " + " + meiliByteLength(conn, "tags.name_key")
	if err := conn.Table("status_tags").Select("status_tags.status_id,COUNT(*) AS count,COALESCE(SUM("+tagSize+"),0) AS bytes,COALESCE(MAX("+tagSize+"),0) AS maximum").Joins("JOIN tags ON tags.id = status_tags.tag_id").Where("status_tags.status_id IN ?", all).Group("status_tags.status_id").Scan(&tags).Error; err != nil {
		return err
	}
	for _, tag := range tags {
		content.Bytes += tag.Bytes
		if tag.Count > 16 || tag.Maximum > 1024 || content.Bytes > meiliDocumentBytes {
			return fmt.Errorf("%w: tags", errMeiliPreflightBudget)
		}
	}
	return nil
}

// meiliPreflightChunks splits an over-budget batch before any preloads. Each
// returned batch passes the collective raw-content, tag and reference limits;
// individually safe roots must not be merged back into an unsafe batch.
// Splitting preserves newest-first order and drops only oversized single roots.
func meiliPreflightChunks(conn *gorm.DB, ids []uint64) ([][]uint64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	err := meiliPreflight(conn, ids)
	if err == nil {
		return [][]uint64{ids}, nil
	}
	if !errors.Is(err, errMeiliPreflightBudget) {
		return nil, err
	}
	if len(ids) == 1 {
		return nil, nil
	}
	middle := len(ids) / 2
	left, err := meiliPreflightChunks(conn, ids[:middle])
	if err != nil {
		return nil, err
	}
	right, err := meiliPreflightChunks(conn, ids[middle:])
	if err != nil {
		return nil, err
	}
	return append(left, right...), nil
}
func meiliSafeBudgetReason(err error) string {
	if err != nil && strings.Contains(err.Error(), "budget") {
		return "resource_budget_exceeded"
	}
	return "build_unavailable"
}
