package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	// DefaultOwnedMeiliIndexMaxBytes caps the recommendation index itself
	// (discovery_statuses_v1_* holds <= 5,000 documents / <= 16 MiB JSON).
	DefaultOwnedMeiliIndexMaxBytes int64 = 512 << 20
	// DefaultSharedMeiliDatabaseMaxBytes is the default whole-engine safety
	// ceiling when Meilisearch (such as bundled v1.12.8) shares one LMDB
	// database across full-text search (accounts, statuses, hashtags) and
	// discovery without reporting per-index byte sizes.
	DefaultSharedMeiliDatabaseMaxBytes int64 = 8 << 30
	// DefaultOwnedMeiliMaxDocuments bounds total documents across owned
	// discovery_statuses_v1_* indexes (at most active + pending generations).
	DefaultOwnedMeiliMaxDocuments int64 = 2 * meiliRowLimit
	meiliDiskHeadroomBytes        int64 = 64 << 20
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
// is optional: v1.12 reports databaseSize + per-index numberOfDocuments (no
// usedDatabaseSize and no per-index byte sizes). v1.15 also reports raw
// document sizes, which exclude index structures. Only full index byte sizes
// can replace the whole-database measurement for disk admission.
type meiliStats struct {
	DatabaseSize     *int64 `json:"databaseSize"`
	UsedDatabaseSize *int64 `json:"usedDatabaseSize"`
	Indexes          map[string]struct {
		NumberOfDocuments *int64 `json:"numberOfDocuments"`
		IndexSize         *int64 `json:"indexSize"`
		UsedIndexSize     *int64 `json:"usedIndexSize"`
	} `json:"indexes"`
}

func positive(v *int64) (int64, bool) {
	if v != nil && *v > 0 {
		return *v, true
	}
	return 0, false
}

type meiliIndexUsage struct {
	ownedBytes, ownedDocuments int64
	hasSharedIndexes           bool
	hasIndexSizes              bool
	missingOwnedSize           bool
}

func (stats meiliStats) indexUsage(prefix string) meiliIndexUsage {
	var usage meiliIndexUsage
	for uid, index := range stats.Indexes {
		size, known := positive(index.UsedIndexSize)
		if !known {
			size, known = positive(index.IndexSize)
		}
		usage.hasIndexSizes = usage.hasIndexSizes || known
		if !strings.HasPrefix(uid, prefix) {
			usage.hasSharedIndexes = true
			continue
		}
		usage.missingOwnedSize = usage.missingOwnedSize || !known
		usage.ownedBytes += size
		docs, _ := positive(index.NumberOfDocuments)
		usage.ownedDocuments += docs
	}
	return usage
}

func (stats meiliStats) budgetBytes(usage meiliIndexUsage, ownedCeiling int64) (used int64, source string, ok bool) {
	// An incomplete subtotal can prove the owned budget is exceeded, but it
	// cannot admit a build. Fall back to the whole-engine measurement then.
	if usage.hasIndexSizes && (!usage.missingOwnedSize || usage.ownedBytes > ownedCeiling-meiliDiskHeadroomBytes) {
		return usage.ownedBytes, "owned_indexes", true
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
	return p.checkDiskBudgetWithSetting(ctx, 0, "")
}

func (p *MeiliProvider) checkDiskBudgetWithSetting(ctx context.Context, configuredMaxMiB int, leaseToken string) error {
	var stats meiliStats
	if err := p.http(ctx, http.MethodGet, "/stats", nil, &stats); err != nil {
		return err
	}
	prefix := p.connection.Prefix + "discovery_statuses_v1_"
	usage := stats.indexUsage(prefix)
	ownedCeiling := DefaultOwnedMeiliIndexMaxBytes
	if p.maxDatabaseBytes > 0 {
		ownedCeiling = min(ownedCeiling, p.maxDatabaseBytes)
	}
	used, source, ok := stats.budgetBytes(usage, ownedCeiling)

	var ceiling int64
	if ok {
		switch {
		case source == "owned_indexes":
			// Keep our per-index budget independent of full-text search
			// indexes and LMDB free pages when per-index sizes are available.
			ceiling = ownedCeiling
		case p.maxDatabaseBytes > 0:
			ceiling = p.maxDatabaseBytes
		case usage.hasSharedIndexes && configuredMaxMiB > 0:
			ceiling = int64(configuredMaxMiB) << 20
		case usage.hasSharedIndexes:
			ceiling = DefaultSharedMeiliDatabaseMaxBytes
		default:
			ceiling = DefaultOwnedMeiliIndexMaxBytes
		}
	}
	p.recordDiskTelemetry(used, ceiling, source, leaseToken)
	if usage.ownedDocuments > DefaultOwnedMeiliMaxDocuments {
		return fmt.Errorf("recommendation search document budget exceeded (%d owned documents of %d)", usage.ownedDocuments, DefaultOwnedMeiliMaxDocuments)
	}
	if !ok {
		// Missing byte sizes still allow rebuilding within the document limit.
		p.warnBudgetUnreadable()
		return nil
	}
	if used > ceiling-meiliDiskHeadroomBytes {
		return fmt.Errorf("recommendation search disk budget exceeded (%d MiB %s of %d MiB)", used>>20, source, ceiling>>20)
	}
	return nil
}

func (p *MeiliProvider) recordDiskTelemetry(used, ceiling int64, source, leaseToken string) {
	p.mu.Lock()
	p.lastDatabaseBytes = used
	p.lastBudgetBytes = ceiling
	p.lastSizeSource = source
	p.mu.Unlock()
	if leaseToken != "" {
		p.writeHealth(leaseToken, "disk", meiliDiskTelemetry{
			Policy:              p.PolicyVersion(),
			DatabaseBytes:       used,
			DatabaseBudgetBytes: ceiling,
			DatabaseSizeSource:  source,
		})
	}
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
