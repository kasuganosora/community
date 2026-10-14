package discovery

import (
	"context"
	"errors"
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
func (p *MeiliProvider) checkDiskBudget(ctx context.Context) error {
	// The server reports the whole shared Meilisearch database, not just our
	// indexes. Leave one bounded build's reserve before admitting any new work.
	ceiling := p.maxDatabaseBytes
	if ceiling <= 0 {
		ceiling = 512 << 20
	}
	var stats struct {
		DatabaseSize *int64 `json:"databaseSize"`
	}
	if err := p.http(ctx, http.MethodGet, "/stats", nil, &stats); err != nil {
		return err
	}
	if stats.DatabaseSize == nil || *stats.DatabaseSize < 0 || *stats.DatabaseSize > ceiling-(64<<20) {
		return errors.New("recommendation search disk budget unavailable")
	}
	return nil
}
func meiliByteLength(conn *gorm.DB, column string) string {
	if dialect.IsSQLite(conn) {
		return "LENGTH(CAST(COALESCE(" + column + ", '') AS BLOB))"
	}
	return "OCTET_LENGTH(COALESCE(" + column + ", ''))"
}

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
						return errors.New("recommendation reference budget exceeded")
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
		return errors.New("recommendation raw content budget exceeded")
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
			return errors.New("recommendation tag budget exceeded")
		}
	}
	return nil
}
func meiliSafeBudgetReason(err error) string {
	if err != nil && strings.Contains(err.Error(), "budget") {
		return "resource_budget_exceeded"
	}
	return "build_unavailable"
}
