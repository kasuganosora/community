package discovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/search"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type meiliHTTPError struct{ Status int }

func (e meiliHTTPError) Error() string {
	return fmt.Sprintf("recommendation search returned HTTP %d", e.Status)
}
func meiliNotFound(err error) bool {
	var target meiliHTTPError
	return errors.As(err, &target) && target.Status == http.StatusNotFound
}

const meiliSchemaVersion = "discovery-v1-rrf60-beta05"
const meiliRowLimit = 5000
const meiliDocumentBytes = 16 << 20
const meiliLeaseTTL = 90 * time.Second

type MeiliStatus struct {
	Ready            bool       `json:"ready"`
	Mode             string     `json:"mode"`
	Reason           string     `json:"reason"`
	Generation       string     `json:"generation,omitempty"`
	Version          string     `json:"version,omitempty"`
	ServerVersion    string     `json:"server_version,omitempty"`
	DocumentCount    int        `json:"document_count"`
	DataCutoff       time.Time  `json:"data_cutoff,omitempty"`
	CheckedAt        time.Time  `json:"checked_at"`
	LastBuildError   string     `json:"last_build_error,omitempty"`
	LastBuildErrorAt *time.Time `json:"last_build_error_at,omitempty"`
	LastQueryError   string     `json:"last_query_error,omitempty"`
	LastQueryErrorAt *time.Time `json:"last_query_error_at,omitempty"`
}
type meiliManifest struct {
	Index         string    `json:"index"`
	Generation    string    `json:"generation"`
	Version       string    `json:"version"`
	ServerVersion string    `json:"server_version"`
	Mode          string    `json:"mode"`
	Documents     int       `json:"documents"`
	Cutoff        time.Time `json:"cutoff"`
	CompletedAt   time.Time `json:"completed_at"`
}
type meiliState struct {
	Active  *meiliManifest `json:"active,omitempty"`
	Pending string         `json:"pending,omitempty"`
	Retired string         `json:"retired,omitempty"`
}
type meiliDocument struct {
	ID          string   `json:"id"`
	AuthorID    string   `json:"author_id"`
	Domain      string   `json:"domain"`
	Text        string   `json:"text"`
	Tags        []string `json:"tags"`
	Language    string   `json:"language"`
	PublishedAt int64    `json:"published_at"`
	Schema      string   `json:"schema"`
}
type meiliEmbedding struct {
	Name            string
	ModelVersion    string
	Configuration   json.RawMessage
	MaxDocuments    int
	MaxUnitsPerHour int
}
type MeiliProvider struct {
	connection       search.DiscoveryConnection
	client           *http.Client
	conn             func() *gorm.DB
	redis            func() redis.UniversalClient
	settings         func(context.Context, *gorm.DB) (systemsettings.DiscoveryConfiguration, error)
	embedding        *meiliEmbedding
	start            sync.Once
	mu               sync.RWMutex
	reason           string
	maxDatabaseBytes int64
}

var currentMeili struct {
	sync.RWMutex
	p *MeiliProvider
}

func NewMeiliProvider() *MeiliProvider {
	p := &MeiliProvider{connection: search.DiscoveryConnectionConfig(), client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, conn: db.GetDB, redis: feedRedis, settings: systemsettings.ReadDiscoveryConfiguration, reason: "not_built"}
	name := strings.TrimSpace(config.C.Other["RECOMMENDATION_EMBEDDER_NAME"])
	version := strings.TrimSpace(config.C.Other["RECOMMENDATION_EMBEDDER_MODEL_VERSION"])
	raw := strings.TrimSpace(config.C.Other["RECOMMENDATION_EMBEDDER_CONFIG"])
	count, _ := strconv.Atoi(config.C.Other["RECOMMENDATION_EMBEDDER_MAX_DOCUMENTS"])
	units, _ := strconv.Atoi(config.C.Other["RECOMMENDATION_EMBEDDER_MAX_UNITS_PER_HOUR"])
	p.maxDatabaseBytes, _ = strconv.ParseInt(config.C.Other["RECOMMENDATION_MEILI_DATABASE_MAX_BYTES"], 10, 64)
	if p.maxDatabaseBytes <= 0 {
		p.maxDatabaseBytes = 512 << 20
	}
	var value map[string]any
	if name != "" && version != "" && count > 0 && count <= meiliRowLimit && units > 0 && units <= 1000000 && config.C.Other["RECOMMENDATION_EMBEDDER_BUDGET_APPROVED"] == "true" && json.Unmarshal([]byte(raw), &value) == nil && len(value) > 0 {
		p.embedding = &meiliEmbedding{Name: name, ModelVersion: version, Configuration: json.RawMessage(raw), MaxDocuments: count, MaxUnitsPerHour: units}
	}
	currentMeili.Lock()
	currentMeili.p = p
	currentMeili.Unlock()
	return p
}
func CurrentMeiliStatus() MeiliStatus {
	currentMeili.RLock()
	p := currentMeili.p
	currentMeili.RUnlock()
	if p == nil {
		return MeiliStatus{Mode: "rules", Reason: "not_initialized", CheckedAt: time.Now().UTC()}
	}
	return p.Status()
}
func (p *MeiliProvider) setReason(reason string) { p.mu.Lock(); p.reason = reason; p.mu.Unlock() }
func (p *MeiliProvider) keys() (string, string) {
	stem := "community:discovery:{meili-" + p.connection.Prefix + "}:"
	return stem + "lease", stem + "state"
}
func (p *MeiliProvider) version(server string) string {
	mode := "meili_lexical"
	if p.embedding != nil {
		mode = "meili_hybrid"
	}
	return p.versionForMode(server, mode)
}

// PolicyVersion binds disposable feed pools/windows to deployment settings.
// It deliberately performs no network request and exposes no configuration.
func (p *MeiliProvider) PolicyVersion() string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%t\x00%s\x00%s\x00%s", p.connection.Enabled, p.connection.Host, p.connection.Prefix, p.version(""))))
	return hex.EncodeToString(hash[:])
}
func (p *MeiliProvider) versionForMode(server, mode string) string {
	value := meiliSchemaVersion + "|" + server + "|lexical|rank=default|tokenizer=server|document=public-7d"
	if p.embedding != nil && mode == "meili_hybrid" {
		value += "|hybrid|" + p.embedding.Name + "|" + p.embedding.ModelVersion + "|" + string(p.embedding.Configuration)
	}
	hash := sha256.Sum256([]byte(value))
	return meiliSchemaVersion + "-" + hex.EncodeToString(hash[:8])
}
func (p *MeiliProvider) Status() MeiliStatus {
	result := MeiliStatus{Mode: "rules", CheckedAt: time.Now().UTC()}
	p.mu.RLock()
	result.Reason = p.reason
	p.mu.RUnlock()
	if !p.connection.Enabled {
		result.Reason = "meilisearch_disabled"
		return result
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	settings, err := p.settings(ctx, p.conn())
	if err != nil {
		result.Reason = "settings_unavailable"
		return result
	}
	if !settings.Enabled {
		result.Reason = "recommendations_disabled"
		return result
	}
	if settings.Mode == "rules" {
		result.Reason = "rules_configured"
		return result
	}
	state, err := p.readState(ctx)
	if err != nil {
		result.Reason = "redis_unavailable"
		return result
	}
	p.operationErrors(ctx, &result)
	if state.Active == nil {
		return result
	}
	a := state.Active
	result.Generation = a.Generation
	result.Version = a.Version
	result.ServerVersion = a.ServerVersion
	result.DocumentCount = a.Documents
	result.DataCutoff = a.Cutoff
	if a.Version != p.versionForMode(a.ServerVersion, a.Mode) {
		result.Reason = "configuration_changed"
		return result
	}
	if time.Since(a.Cutoff) >= PoolMaxAge || a.Cutoff.After(time.Now().UTC()) {
		result.Reason = "index_expired"
		return result
	}
	if settings.ApprovedVersion != a.Version {
		result.Reason = "quality_not_approved"
		return result
	}
	result.Ready = true
	result.Mode = a.Mode
	result.Reason = "ready"
	return result
}
func (p *MeiliProvider) Start(ctx context.Context) {
	p.start.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				_ = p.rebuild(ctx)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}

var meiliWriteStateScript = redis.NewScript(`if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end redis.call('SET',KEYS[2],ARGV[2]) return 1`)
var meiliReleaseScript = redis.NewScript(`if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`)

func (p *MeiliProvider) readState(ctx context.Context) (meiliState, error) {
	var state meiliState
	client := p.redis()
	if client == nil {
		return state, ErrUnavailable
	}
	_, key := p.keys()
	data, err := client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if len(data) > 8192 {
		return state, ErrUnavailable
	}
	err = json.Unmarshal(data, &state)
	return state, err
}
func (p *MeiliProvider) writeState(ctx context.Context, token string, state meiliState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	lease, key := p.keys()
	n, err := meiliWriteStateScript.Run(ctx, p.redis(), []string{lease, key}, token, string(data)).Int()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrUnavailable
	}
	return nil
}

// One deployment lease covers index writes, retirement and all candidate
// searches. A cancelled or expired owner cannot publish a manifest.
func (p *MeiliProvider) lease(parent context.Context, budget time.Duration) (context.Context, string, func(), error) {
	client := p.redis()
	if client == nil {
		return nil, "", nil, ErrUnavailable
	}
	token, err := feedGeneration()
	if err != nil {
		return nil, "", nil, err
	}
	key, _ := p.keys()
	ctx, cancel := context.WithTimeout(parent, budget)
	ok, err := client.SetNX(ctx, key, token, meiliLeaseTTL).Result()
	if err != nil || !ok {
		cancel()
		if err == nil {
			err = ErrUnavailable
		}
		return nil, "", nil, err
	}
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				value, err := client.Get(ctx, key).Result()
				if err != nil || value != token {
					cancel()
					return
				}
			}
		}
	}()
	release := func() {
		cancel()
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = meiliReleaseScript.Run(cleanup, client, []string{key}, token).Err()
	}
	return ctx, token, release, nil
}
func (p *MeiliProvider) http(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.connection.Host, "/")+path, reader)
	if err != nil {
		return errors.New("invalid recommendation search endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	if p.connection.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.connection.APIKey)
	}
	response, err := p.client.Do(req)
	if err != nil {
		return errors.New("recommendation search transport unavailable")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return meiliHTTPError{Status: response.StatusCode}
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}
func (p *MeiliProvider) task(ctx context.Context, method, path string, body any) error {
	var ack struct {
		TaskUID *int64 `json:"taskUid"`
	}
	if err := p.http(ctx, method, path, body, &ack); err != nil {
		return err
	}
	if ack.TaskUID == nil {
		return errors.New("missing recommendation index task")
	}
	ticker := time.NewTicker(40 * time.Millisecond)
	defer ticker.Stop()
	for {
		var task struct {
			Status string `json:"status"`
		}
		if err := p.http(ctx, http.MethodGet, "/tasks/"+strconv.FormatInt(*ack.TaskUID, 10), nil, &task); err != nil {
			return err
		}
		switch task.Status {
		case "succeeded":
			return nil
		case "failed", "canceled":
			return errors.New("recommendation index task did not succeed")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (p *MeiliProvider) retire(ctx context.Context, index string) error {
	if index == "" {
		return nil
	}
	if !strings.HasPrefix(index, p.connection.Prefix+"discovery_statuses_v1_") {
		return errors.New("invalid recommendation index ownership")
	}
	// Enqueued asynchronous writes survive caller cancellation. Do not create a
	// third generation until the previous build has drained and is deleted.
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		var tasks struct {
			Results []json.RawMessage `json:"results"`
		}
		if err := p.http(ctx, http.MethodGet, "/tasks?indexUids="+url.QueryEscape(index)+"&statuses=enqueued,processing&limit=1", nil, &tasks); err != nil {
			return err
		}
		if len(tasks.Results) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	var metadata map[string]any
	if err := p.http(ctx, http.MethodGet, "/indexes/"+url.PathEscape(index), nil, &metadata); err != nil {
		if meiliNotFound(err) {
			return nil
		}
		return err
	}
	return p.task(ctx, http.MethodDelete, "/indexes/"+url.PathEscape(index), nil)
}
func (p *MeiliProvider) documents(ctx context.Context, asOf time.Time) ([]meiliDocument, error) {
	conn := p.conn()
	if conn == nil {
		return nil, ErrUnavailable
	}
	rows := []meiliDocument{}
	used := 0
	isolation := sql.LevelRepeatableRead
	if dialect.IsSQLite(conn) {
		isolation = sql.LevelSerializable
	}
	err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ids []uint64
		if err := models.RecommendationStatusScope(tx, asOf, CandidateWindow).Select("statuses.id").Order("statuses.created_at, statuses.id").Limit(meiliRowLimit + 1).Scan(&ids).Error; err != nil {
			return err
		}
		if len(ids) > meiliRowLimit {
			return errors.New("recommendation index row budget exceeded")
		}
		if err := meiliPreflight(tx, ids); err != nil {
			return err
		}
		for offset := 0; offset < len(ids); offset += CandidateLimit {
			end := offset + CandidateLimit
			if end > len(ids) {
				end = len(ids)
			}
			stubs := make(models.Statuses, 0, end-offset)
			for _, id := range ids[offset:end] {
				stubs = append(stubs, &models.Status{Id: id})
			}
			eligible, err := models.FilterRecommendableStatuses(tx, nil, stubs, models.RecommendationEligibilityOptions{Context: "public", AsOf: asOf, Window: CandidateWindow, RootWindow: true})
			if err != nil {
				return err
			}
			for _, s := range eligible {
				if s.CreatedAt == nil || s.Account == nil {
					continue
				}
				domain := ""
				if s.Account.Domain != nil {
					domain = *s.Account.Domain
				}
				tags := make([]string, 0, len(s.Tags))
				for _, tag := range s.Tags {
					tags = append(tags, models.TagKey(tag.Name))
				}
				text := htmlsafe.PlainText(s.Content.SpoilerText + "\n" + s.Content.Content)
				if len(text) > 16384 {
					return errors.New("recommendation document text budget exceeded")
				}
				doc := meiliDocument{ID: strconv.FormatUint(s.Id, 10), AuthorID: strconv.FormatUint(s.AccountId, 10), Domain: domain, Text: text, Tags: tags, Language: s.Language, PublishedAt: s.CreatedAt.Unix(), Schema: meiliSchemaVersion}
				serialized, err := json.Marshal(doc)
				if err != nil {
					return err
				}
				used += len(serialized) + 1
				if used > meiliDocumentBytes {
					return errors.New("recommendation JSON document budget exceeded")
				}
				rows = append(rows, doc)
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: isolation, ReadOnly: true})
	return rows, err
}
func (p *MeiliProvider) rebuild(parent context.Context) error {
	if !p.connection.Enabled {
		p.setReason("meilisearch_disabled")
		return nil
	}
	settingsCtx, settingsCancel := context.WithTimeout(parent, time.Second)
	settings, err := p.settings(settingsCtx, p.conn())
	settingsCancel()
	if err != nil {
		p.setReason("settings_unavailable")
		return err
	}
	if !settings.Enabled || settings.Mode == "rules" {
		p.setReason("rules_configured")
		return nil
	}
	ctx, token, release, err := p.lease(parent, 70*time.Second)
	if err != nil {
		return err
	}
	defer release()
	err = p.buildOwned(ctx, token)
	p.recordOperation(token, "build", err)
	if err != nil {
		p.setReason(meiliSafeBudgetReason(err))
	}
	return err
}
func (p *MeiliProvider) buildOwned(ctx context.Context, token string) error {
	state, err := p.readState(ctx)
	if err != nil {
		return err
	}
	var version struct {
		Version string `json:"pkgVersion"`
	}
	if err = p.http(ctx, http.MethodGet, "/version", nil, &version); err != nil {
		return err
	}
	if version.Version == "" {
		return errors.New("missing recommendation search version")
	}
	// Clear abandoned generations first. Never delete an active generation.
	for _, index := range []string{state.Pending, state.Retired} {
		if index == "" {
			continue
		}
		if state.Active != nil && index == state.Active.Index {
			return errors.New("invalid recommendation index lifecycle")
		}
		cleanup, cancel := context.WithTimeout(ctx, 8*time.Second)
		err = p.retire(cleanup, index)
		cancel()
		if err != nil {
			return err
		}
		if index == state.Pending {
			state.Pending = ""
		}
		if index == state.Retired {
			state.Retired = ""
		}
		if err = p.writeState(ctx, token, state); err != nil {
			return err
		}
	}
	// Redis can lose its disposable state. Reconcile only our namespaced
	// recommendation indexes before opening another generation, including
	// asynchronous orphan builds from a cancelled predecessor.
	if err = p.reconcileOwned(ctx, state); err != nil {
		return err
	}
	if state.Active != nil {
		var metadata map[string]any
		if check := p.http(ctx, http.MethodGet, "/indexes/"+url.PathEscape(state.Active.Index), nil, &metadata); check != nil {
			if !meiliNotFound(check) {
				return check
			}
			state.Active = nil
			if err = p.writeState(ctx, token, state); err != nil {
				return err
			}
		}
	}
	fingerprint := p.version(version.Version)
	if state.Active != nil && state.Active.Version == p.versionForMode(version.Version, state.Active.Mode) && time.Since(state.Active.Cutoff) < 2*time.Minute {
		return nil
	}
	if err = p.checkDiskBudget(ctx); err != nil {
		return err
	}
	cutoff := time.Now().UTC()
	extract, cancel := context.WithTimeout(ctx, 5*time.Second)
	documents, err := p.documents(extract, cutoff)
	cancel()
	if err != nil {
		return err
	}
	mode := "meili_lexical"
	if p.embedding != nil && len(documents) <= p.embedding.MaxDocuments && p.reserveEmbedding(ctx, len(documents)+1) {
		mode = "meili_hybrid"
	}
	fingerprint = p.versionForMode(version.Version, mode)
	generation, err := feedGeneration()
	if err != nil {
		return err
	}
	index := p.connection.Prefix + "discovery_statuses_v1_" + generation
	state.Pending = index
	if err = p.writeState(ctx, token, state); err != nil {
		return err
	}
	build, buildCancel := context.WithTimeout(ctx, 60*time.Second)
	defer buildCancel()
	if err = p.task(build, http.MethodPost, "/indexes", map[string]any{"uid": index, "primaryKey": "id"}); err != nil {
		return err
	}
	settings := map[string]any{"searchableAttributes": []string{"text", "tags"}, "filterableAttributes": []string{"tags", "language", "published_at", "schema"}, "sortableAttributes": []string{"published_at"}, "displayedAttributes": []string{"id"}, "pagination": map[string]int{"maxTotalHits": 5000}}
	if mode == "meili_hybrid" {
		settings["embedders"] = map[string]any{p.embedding.Name: p.embedding.Configuration}
	}
	if err = p.task(build, http.MethodPatch, "/indexes/"+index+"/settings", settings); err != nil {
		return err
	}
	for offset := 0; offset < len(documents); offset += 250 {
		end := offset + 250
		if end > len(documents) {
			end = len(documents)
		}
		if err = p.task(build, http.MethodPost, "/indexes/"+index+"/documents", documents[offset:end]); err != nil {
			return err
		}
	}
	// Probe actual server query/schema readiness, including an empty index.
	var probe map[string]any
	if err = p.http(build, http.MethodPost, "/indexes/"+index+"/search", map[string]any{"q": "", "limit": 1, "filter": "schema = " + strconv.Quote(meiliSchemaVersion)}, &probe); err != nil {
		return err
	}
	if mode == "meili_hybrid" {
		// A configured embedder and successful settings task do not prove query
		// embeddings work. This constant public probe is not a user's interest.
		if err = p.http(build, http.MethodPost, "/indexes/"+index+"/search", map[string]any{"q": "community", "limit": 1, "hybrid": map[string]any{"embedder": p.embedding.Name, "semanticRatio": 0.5}}, &probe); err != nil {
			return err
		}
	}
	if time.Since(cutoff) >= PoolMaxAge {
		return ErrUnavailable
	}
	active := &meiliManifest{Index: index, Generation: generation, Version: fingerprint, ServerVersion: version.Version, Mode: mode, Documents: len(documents), Cutoff: cutoff, CompletedAt: time.Now().UTC()}
	if state.Active != nil {
		state.Retired = state.Active.Index
	}
	state.Active = active
	state.Pending = ""
	if err = p.writeState(ctx, token, state); err != nil {
		return err
	}
	p.setReason("built")
	if state.Retired != "" {
		cleanup, cancel := context.WithTimeout(ctx, 8*time.Second)
		err = p.retire(cleanup, state.Retired)
		cancel()
		if err != nil {
			return err
		}
		state.Retired = ""
		return p.writeState(ctx, token, state)
	}
	return nil
}

// BuildBuckets only searches a frozen, ready generation. The shared lease
// gives this entire batch at most two search slots across all API replicas.
func (p *MeiliProvider) BuildBuckets(parent context.Context, asOf time.Time, buckets []ContentBucket) (_ ContentBatch, returnErr error) {
	empty := ContentBatch{Mode: "rules", Lists: []ContentList{}}
	if !p.connection.Enabled {
		return empty, ErrUnavailable
	}
	// Candidate and index refresh timers may align. Wait within this background
	// batch's existing deadline rather than starving every candidate generation
	// while a healthy index writer briefly owns the deployment lease.
	batchContext, cancelBatch := context.WithTimeout(parent, 30*time.Second)
	defer cancelBatch()
	waitUntil := time.Now().Add(20 * time.Second)
	var ctx context.Context
	var token string
	var release func()
	var err error
	for {
		ctx, token, release, err = p.lease(batchContext, 30*time.Second)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrUnavailable) || !time.Now().Before(waitUntil) {
			return empty, err
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-batchContext.Done():
			timer.Stop()
			return empty, batchContext.Err()
		case <-timer.C:
		}
	}
	defer release()
	settings, err := p.settings(ctx, p.conn())
	if err != nil || !settings.Enabled || settings.Mode != "auto" {
		return empty, ErrUnavailable
	}
	state, err := p.readState(ctx)
	if err != nil || state.Active == nil {
		return empty, ErrUnavailable
	}
	active := state.Active
	now := time.Now().UTC()
	if asOf.IsZero() {
		asOf = now
	}
	if asOf.After(now.Add(time.Second)) || active.Cutoff.After(now) || now.Sub(active.Cutoff) >= PoolMaxAge || active.Version != p.versionForMode(active.ServerVersion, active.Mode) || settings.ApprovedVersion != active.Version {
		return empty, ErrUnavailable
	}
	if len(buckets) > 128 {
		return empty, ErrInvalidRequest
	}
	unique := make([]ContentBucket, 0, len(buckets))
	seen := map[string]bool{}
	for _, bucket := range buckets {
		if bucket.Key == "" || len(bucket.Key) > 256 || len(bucket.Query) > 256 || len(bucket.Language) > 32 {
			return empty, ErrInvalidRequest
		}
		key := models.TagKey(bucket.Key) + "\x00" + strings.TrimSpace(bucket.Query) + "\x00" + bucket.Language
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, bucket)
	}
	defer func() { p.recordOperation(token, "query", returnErr) }()
	type bucketResult struct {
		Index int
		Lists []ContentList
		Err   error
	}
	jobs := make(chan int)
	results := make(chan bucketResult, len(unique))
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				lists, err := p.searchBucket(ctx, *active, asOf, unique[index])
				results <- bucketResult{index, lists, err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for index := range unique {
			select {
			case jobs <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { workers.Wait(); close(results) }()
	ordered := make([][]ContentList, len(unique))
	completed := 0
	for result := range results {
		if result.Err != nil {
			err = result.Err
		}
		ordered[result.Index] = result.Lists
		completed++
	}
	if err != nil || ctx.Err() != nil || completed != len(unique) {
		p.setReason("query_unavailable")
		return empty, ErrUnavailable
	}
	// SQL configuration is checked again before publication. A quality or mode
	// revocation while the remote queries ran must not enable the enhancement.
	settings, err = p.settings(ctx, p.conn())
	if err != nil || !settings.Enabled || settings.Mode != "auto" || settings.ApprovedVersion != active.Version {
		return empty, ErrUnavailable
	}
	leaseKey, _ := p.keys()
	owner, leaseErr := p.redis().Get(ctx, leaseKey).Result()
	if leaseErr != nil || owner != token || time.Since(active.Cutoff) >= PoolMaxAge {
		return empty, ErrUnavailable
	}
	batch := ContentBatch{Mode: active.Mode, Generation: active.Generation, DataCutoff: active.Cutoff, Lists: []ContentList{}}
	for _, lists := range ordered {
		batch.Lists = append(batch.Lists, lists...)
	}
	p.setReason("ready")
	return batch, nil
}
func (p *MeiliProvider) searchBucket(parent context.Context, active meiliManifest, asOf time.Time, bucket ContentBucket) ([]ContentList, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	filter := []string{"published_at >= " + strconv.FormatInt(asOf.Add(-CandidateWindow).Unix(), 10), "published_at < " + strconv.FormatInt(asOf.Unix(), 10), "schema = " + strconv.Quote(meiliSchemaVersion)}
	if bucket.Language != "" {
		filter = append(filter, "language = "+strconv.Quote(bucket.Language))
	}
	families := []string{"exact", "lexical"}
	if active.Mode == "meili_hybrid" {
		if p.embedding == nil {
			return nil, ErrUnavailable
		}
		families = append(families, "semantic")
	}
	lists := make([]ContentList, 0, len(families))
	for _, family := range families {
		request := map[string]any{"q": bucket.Query, "limit": 80, "attributesToRetrieve": []string{"id"}, "filter": filter}
		if family == "exact" {
			request["q"] = ""
			request["sort"] = []string{"published_at:desc"}
			exact := append([]string(nil), filter...)
			request["filter"] = append(exact, "tags = "+strconv.Quote(models.TagKey(bucket.Key)))
		}
		// Pure lexical requests OMIT hybrid entirely. A zero semantic ratio using
		// an omitempty SDK field would accidentally select the server default.
		if family == "semantic" {
			if !p.reserveEmbedding(ctx, 1) {
				return nil, ErrUnavailable
			}
			request["hybrid"] = map[string]any{"embedder": p.embedding.Name, "semanticRatio": 0.5}
		}
		var response struct {
			Hits []struct {
				ID string `json:"id"`
			} `json:"hits"`
		}
		if err := p.http(ctx, http.MethodPost, "/indexes/"+url.PathEscape(active.Index)+"/search", request, &response); err != nil {
			return nil, err
		}
		ids := make([]uint64, 0, len(response.Hits))
		seen := map[uint64]bool{}
		for _, hit := range response.Hits {
			if len(ids) >= 80 {
				break
			}
			id, err := strconv.ParseUint(hit.ID, 10, 64)
			if err != nil || id == 0 || seen[id] {
				continue
			}
			seen[id] = true
			ids = append(ids, id)
		}
		lists = append(lists, ContentList{BucketKey: bucket.Key, Family: family, Key: bucket.Query, Generation: active.Generation, IDs: ids})
	}
	return lists, nil
}

func (p *MeiliProvider) reconcileOwned(ctx context.Context, state meiliState) error {
	prefix := p.connection.Prefix + "discovery_statuses_v1_"
	for offset := 0; offset < 1000; offset += 100 {
		var page struct {
			Results []struct {
				UID string `json:"uid"`
			} `json:"results"`
			Total int `json:"total"`
		}
		if err := p.http(ctx, http.MethodGet, "/indexes?limit=100&offset="+strconv.Itoa(offset), nil, &page); err != nil {
			return err
		}
		// Snapshot names before deleting: deleting while advancing offset can
		// skip entries. At most two owned generations should exist; if there are
		// more, fail closed for manual repair rather than making more indexes.
		if page.Total > 1000 {
			return errors.New("recommendation index inventory budget exceeded")
		}
		orphan := []string{}
		for _, item := range page.Results {
			if !strings.HasPrefix(item.UID, prefix) {
				continue
			}
			if state.Active != nil && item.UID == state.Active.Index {
				continue
			}
			if item.UID == state.Pending || item.UID == state.Retired {
				continue
			}
			orphan = append(orphan, item.UID)
		}
		for _, index := range orphan {
			if err := p.retire(ctx, index); err != nil {
				return err
			}
		}
		if len(orphan) > 0 {
			return p.reconcileOwned(ctx, state)
		}
		if offset+len(page.Results) >= page.Total {
			return nil
		}
	}
	return errors.New("recommendation index inventory budget exceeded")
}
