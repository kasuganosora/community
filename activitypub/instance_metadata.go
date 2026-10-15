package activitypub

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Instance metadata (nodeinfo name/software/version + icon) is cached per
// domain in instance_metadata. Successful lookups are kept for 24h; failures
// back off exponentially (10m, 20m, … capped at 7d). Remote reaction software
// detection and the admin instance list share this one cache.
const (
	instanceMetadataTTL         = 24 * time.Hour
	instanceMetadataBackoffBase = 10 * time.Minute
	instanceMetadataBackoffMax  = 7 * 24 * time.Hour
	instanceMetadataBatch       = 8
	instanceMetadataConcurrency = 2
	instanceMetadataInterval    = time.Minute
)

var instanceHostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?(:[0-9]{1,5})?$`)

// instanceMetadataNow is replaceable by tests.
var instanceMetadataNow = time.Now

func instanceBackoff(failures int) time.Duration {
	d := instanceMetadataBackoffBase
	for i := 1; i < failures && d < instanceMetadataBackoffMax; i++ {
		d *= 2
	}
	if d > instanceMetadataBackoffMax {
		d = instanceMetadataBackoffMax
	}
	return d
}

func metadataDB() *gorm.DB {
	conn := db.GetDB()
	if conn == nil || !conn.Migrator().HasTable(&models.InstanceMetadata{}) {
		return nil
	}
	return conn
}

// cachedInstanceMetadata returns the stored row and whether it may be used
// without a new lookup (fresh success, or still inside the failure backoff).
func cachedInstanceMetadata(ctx context.Context, conn *gorm.DB, host string) (*models.InstanceMetadata, bool) {
	if conn == nil {
		return nil, false
	}
	var row models.InstanceMetadata
	if conn.WithContext(ctx).Where("domain = ?", host).Take(&row).Error != nil {
		return nil, false
	}
	now := instanceMetadataNow()
	if row.NextAttemptAt != nil && row.NextAttemptAt.After(now) {
		return &row, true
	}
	return &row, false
}

// fetchNodeInfo performs the nodeinfo lookup through the SSRF-safe client.
func fetchNodeInfo(ctx context.Context, host string, full bool) (models.InstanceMetadata, error) {
	meta := models.InstanceMetadata{Domain: host}
	base := remoteReactionBase(host)
	var wk struct {
		Links []struct {
			Rel  string `json:"rel"`
			Href string `json:"href"`
		} `json:"links"`
	}
	if err := remoteGetJSON(ctx, http.MethodGet, base+"/.well-known/nodeinfo", nil, &wk); err != nil {
		return meta, err
	}
	href := ""
	for _, l := range wk.Links {
		if strings.HasPrefix(l.Rel, "http://nodeinfo.diaspora.software/ns/schema/") {
			href = l.Href
		}
	}
	b, _ := url.Parse(base)
	u, perr := url.Parse(href)
	if perr != nil || href == "" || !strings.EqualFold(u.Host, b.Host) {
		return meta, &url.Error{Op: "nodeinfo", URL: base, Err: errNoNodeInfo}
	}
	var ni struct {
		Software struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"software"`
		Metadata map[string]any `json:"metadata"`
	}
	if err := remoteGetJSON(ctx, http.MethodGet, href, nil, &ni); err != nil {
		return meta, err
	}
	meta.Software = clip(strings.ToLower(ni.Software.Name), 64)
	meta.Version = clip(ni.Software.Version, 64)
	if s, ok := ni.Metadata["nodeName"].(string); ok {
		meta.Name = clip(strings.TrimSpace(s), 255)
	}
	meta.IconURL = base + "/favicon.ico"
	if full && softwareFamily(meta.Software) == softwareMisskey {
		var m struct {
			IconURL string `json:"iconUrl"`
			Name    string `json:"name"`
		}
		if remoteGetJSON(ctx, http.MethodPost, base+"/api/meta", map[string]any{"detail": false}, &m) == nil {
			if iu, e := url.Parse(m.IconURL); e == nil && (iu.Scheme == "https" || iu.Scheme == "http") && len(m.IconURL) <= 1024 {
				meta.IconURL = m.IconURL
			}
			if meta.Name == "" {
				meta.Name = clip(strings.TrimSpace(m.Name), 255)
			}
		}
	}
	return meta, nil
}

type nodeInfoError string

func (e nodeInfoError) Error() string { return string(e) }

const errNoNodeInfo = nodeInfoError("no nodeinfo link")

func clip(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return s
}

// refreshInstanceMetadata fetches and persists metadata for host, recording
// failures with exponential backoff. The previous good data is kept on failure.
func refreshInstanceMetadata(ctx context.Context, conn *gorm.DB, host string, prev *models.InstanceMetadata, full bool) models.InstanceMetadata {
	meta, err := fetchNodeInfo(ctx, host, full)
	now := instanceMetadataNow()
	if err != nil {
		if prev != nil {
			meta = *prev
		}
		meta.Domain = host
		meta.FailCount++
		meta.FailedAt = &now
		next := now.Add(instanceBackoff(meta.FailCount))
		meta.NextAttemptAt = &next
	} else {
		if !full && prev != nil && prev.IconURL != "" {
			meta.IconURL = prev.IconURL
		}
		meta.FetchedAt = &now
		meta.FailCount = 0
		meta.FailedAt = nil
		next := now.Add(instanceMetadataTTL)
		meta.NextAttemptAt = &next
	}
	if conn != nil {
		if e := conn.WithContext(context.WithoutCancel(ctx)).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "domain"}}, UpdateAll: true}).Create(&meta).Error; e != nil {
			log.Warn("instance metadata: store %s: %v", host, e)
		}
	}
	return meta
}

var instanceLookupMu sync.Map // host -> *sync.Mutex, collapses concurrent lookups

// InstanceMetadataFor returns cached metadata, fetching (bounded, SSRF-safe)
// only when the cache is stale and the host is outside its failure backoff.
// The synchronous path (remote reactions) does only the two nodeinfo
// requests; the background worker also asks Misskey forks for their icon.
func InstanceMetadataFor(ctx context.Context, host string) models.InstanceMetadata {
	return instanceMetadataFor(ctx, host, false)
}

func instanceMetadataFor(ctx context.Context, host string, full bool) models.InstanceMetadata {
	host = strings.ToLower(strings.TrimSpace(host))
	if !instanceHostPattern.MatchString(host) {
		return models.InstanceMetadata{Domain: host}
	}
	m, _ := instanceLookupMu.LoadOrStore(host, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	conn := metadataDB()
	row, fresh := cachedInstanceMetadata(ctx, conn, host)
	if fresh {
		return *row
	}
	return refreshInstanceMetadata(ctx, conn, host, row, full)
}

// InstanceMetadataWorker refreshes stale/missing metadata in small batches.
type InstanceMetadataWorker struct {
	stop chan struct{}
	wg   sync.WaitGroup
}

func (w *InstanceMetadataWorker) Stop() {
	if w == nil {
		return
	}
	close(w.stop)
	w.wg.Wait()
}

// StartInstanceMetadataWorker launches the background refresher. It is a
// no-op (nil worker) when the table has not been migrated yet.
func StartInstanceMetadataWorker() *InstanceMetadataWorker {
	if metadataDB() == nil {
		log.Warn("instance metadata table missing; run migrations to enable instance metadata")
		return nil
	}
	w := &InstanceMetadataWorker{stop: make(chan struct{})}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		t := time.NewTicker(instanceMetadataInterval)
		defer t.Stop()
		for {
			select {
			case <-w.stop:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
				RefreshInstanceMetadataBatch(ctx)
				cancel()
			}
		}
	}()
	return w
}

// dueInstanceDomains picks missing domains first, then stale/backed-off ones
// whose next attempt is due.
func dueInstanceDomains(ctx context.Context, conn *gorm.DB, limit int) []string {
	var hosts []string
	conn = conn.WithContext(ctx)
	conn.Model(&models.Account{}).Distinct("domain").
		Where("domain IS NOT NULL AND domain <> '' AND domain NOT IN (?)", conn.Model(&models.InstanceMetadata{}).Select("domain")).
		Limit(limit).Pluck("domain", &hosts)
	if len(hosts) < limit {
		var due []string
		conn.Model(&models.InstanceMetadata{}).Where("next_attempt_at IS NULL OR next_attempt_at <= ?", instanceMetadataNow()).
			Order("next_attempt_at").Limit(limit-len(hosts)).Pluck("domain", &due)
		hosts = append(hosts, due...)
	}
	return hosts
}

// RefreshInstanceMetadataBatch refreshes one batch with low concurrency.
func RefreshInstanceMetadataBatch(ctx context.Context) int {
	conn := metadataDB()
	if conn == nil {
		return 0
	}
	hosts := dueInstanceDomains(ctx, conn, instanceMetadataBatch)
	sem := make(chan struct{}, instanceMetadataConcurrency)
	var wg sync.WaitGroup
	n := 0
	for _, h := range hosts {
		h = strings.ToLower(h)
		if !instanceHostPattern.MatchString(h) {
			continue
		}
		n++
		wg.Add(1)
		sem <- struct{}{}
		go func(h string) {
			defer wg.Done()
			defer func() { <-sem }()
			instanceMetadataFor(ctx, h, true)
		}(h)
	}
	wg.Wait()
	return n
}
