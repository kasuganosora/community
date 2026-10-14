package discovery

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

var ErrTagCursor = errors.New("discovery: tag cursor reset required")

type tagTrendSnapshot struct {
	Generation string    `json:"generation"`
	Window     string    `json:"window"`
	Scope      string    `json:"scope"`
	Kind       string    `json:"kind"`
	AsOf       time.Time `json:"as_of"`
	Keys       []string  `json:"keys"`
	Partial    bool      `json:"partial,omitempty"`
}
type tagTrendCursor struct {
	Generation string `json:"g"`
	Window     string `json:"w"`
	Scope      string `json:"s"`
	Viewer     uint64 `json:"u"`
	Position   int    `json:"p"`
	Expires    int64  `json:"e"`
}
type TagTrendPage struct {
	Items            []TagTrend `json:"items"`
	NextCursor       *string    `json:"next_cursor"`
	AsOf             time.Time  `json:"as_of"`
	CheckedAt        time.Time  `json:"checked_at"`
	Window           string     `json:"window"`
	Scope            string     `json:"scope"`
	Kind             string     `json:"kind"`
	Version          string     `json:"version"`
	CoverageComplete bool       `json:"coverage_complete"`
	Disabled         bool       `json:"disabled"`
}

func tagSnapshotKey(generation string) string {
	return "community:discovery:tags:snapshot:" + generation
}
func tagLatestKey(window, scope string) string {
	return "community:discovery:{tags:" + window + ":" + scope + "}:latest"
}

func readTagSnapshot(ctx context.Context, key string, dest *tagTrendSnapshot) error {
	client := feedRedis()
	if client == nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	data, err := client.Get(ctx, key).Bytes()
	if err != nil {
		return err
	}
	if len(data) > 64<<10 {
		return ErrUnavailable
	}
	return json.Unmarshal(data, dest)
}

func buildTagSnapshot(ctx context.Context, window, scope string) (*tagTrendSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Deployment-wide per-bucket lease prevents all API replicas rebuilding
	// the same aggregate on a cold cache. Token-checked release cannot remove
	// a successor's lease.
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(random)
	r := feedRedis()
	if r == nil {
		return nil, ErrUnavailable
	}
	lock := tagLatestKey(window, scope) + ":lock"
	locked, err := r.SetNX(ctx, lock, token, 8*time.Second).Result()
	if err != nil {
		return nil, err
	}
	if !locked {
		return nil, ErrTrendBusy
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = r.Eval(releaseCtx, `if redis.call('get', KEYS[1]) == ARGV[1] then return redis.call('del', KEYS[1]) end return 0`, []string{lock}, token).Err()
	}()
	asOf := time.Now().UTC()
	rows, partial, err := AggregateTagTrendsPartial(ctx, db.GetDB(), nil, window, scope, asOf, nil)
	if err != nil {
		return nil, err
	}
	snap := &tagTrendSnapshot{Generation: token, Window: window, Scope: scope, Kind: "recent", AsOf: asOf, Keys: []string{}, Partial: partial}
	for _, row := range rows {
		if row.AuthorsCount >= TagTrendMinAuthors {
			snap.Kind = "trending"
			break
		}
	}
	for _, row := range rows {
		if snap.Kind == "trending" && row.AuthorsCount < TagTrendMinAuthors {
			continue
		}
		snap.Keys = append(snap.Keys, row.key)
		if len(snap.Keys) == 200 {
			break
		}
	}
	current, err := r.Get(ctx, lock).Result()
	if err != nil || current != token {
		return nil, ErrTrendBusy
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}
	if err = r.Set(ctx, tagSnapshotKey(token), data, 10*time.Minute).Err(); err != nil {
		return nil, err
	}
	// Publication is fenced on the lease token. Lock/latest keys have the
	// same Redis hash tag, so this also works on Redis Cluster.
	published, err := r.Eval(ctx, `if redis.call('get', KEYS[1]) ~= ARGV[1] then return 0 end redis.call('set', KEYS[2], ARGV[2], 'EX', 300) return 1`, []string{lock, tagLatestKey(window, scope)}, token, string(data)).Int()
	if err != nil {
		return nil, err
	}
	if published != 1 {
		return nil, ErrTrendBusy
	}
	return snap, nil
}

// RefreshTagTrends is called by the normal background scheduler. Four fixed
// buckets are the complete key space; user input cannot create new buckets.
func RefreshTagTrends(ctx context.Context) error {
	var failures []error
	for _, window := range []string{"24h", "7d"} {
		for _, scope := range []string{"local", "federated"} {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Each window/scope bucket is built independently; one bucket
			// failing never blocks the others. A held lease means another
			// replica is already rebuilding that bucket.
			if _, err := buildTagSnapshot(ctx, window, scope); err != nil && !errors.Is(err, ErrTrendBusy) {
				failures = append(failures, fmt.Errorf("%s/%s: %w", window, scope, err))
			}
		}
	}
	return errors.Join(failures...)
}

func signTagCursor(cursor tagTrendCursor) (string, error) {
	keys := CursorSigningKeys()
	if len(keys) == 0 {
		return "", errors.New("discovery cursor signing is not configured")
	}
	body, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, keys[0])
	mac.Write([]byte("tag-trends:"))
	mac.Write(body)
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
func parseTagCursor(raw string) (tagTrendCursor, error) {
	var cursor tagTrendCursor
	if len(raw) > 1024 {
		return cursor, ErrTagCursor
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return cursor, ErrTagCursor
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return cursor, ErrTagCursor
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return cursor, ErrTagCursor
	}
	valid := false
	for _, key := range CursorSigningKeys() {
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte("tag-trends:"))
		mac.Write(body)
		valid = valid || hmac.Equal(sig, mac.Sum(nil))
	}
	if !valid || json.Unmarshal(body, &cursor) != nil || cursor.Position < 0 || cursor.Position > 200 || cursor.Expires <= time.Now().Unix() {
		return cursor, ErrTagCursor
	}
	return cursor, nil
}

func ReadTagTrends(ctx context.Context, viewer *models.Account, window, scope string, limit int, rawCursor string) (*TagTrendPage, error) {
	if _, ok := trendWindow(window); !ok {
		return nil, fmt.Errorf("invalid window")
	}
	if scope != "local" && scope != "federated" {
		return nil, fmt.Errorf("invalid scope")
	}
	limit = max(1, min(limit, 20))
	settings, err := systemsettings.ReadDiscoveryConfiguration(ctx, db.GetDB())
	if err != nil {
		return nil, err
	}
	if !settings.TrendsEnabled {
		return &TagTrendPage{Items: []TagTrend{}, Window: window, Scope: scope, Kind: "recent", Version: TagTrendVersion, Disabled: true}, nil
	}

	viewerID := uint64(0)
	if viewer != nil {
		viewerID = viewer.Id
	}
	var snap tagTrendSnapshot
	position := 0
	if rawCursor != "" {
		cursor, err := parseTagCursor(rawCursor)
		if err != nil {
			return nil, err
		}
		if cursor.Viewer != viewerID || cursor.Window != window || cursor.Scope != scope {
			return nil, ErrTagCursor
		}
		if err = readTagSnapshot(ctx, tagSnapshotKey(cursor.Generation), &snap); err != nil {
			return nil, ErrTagCursor
		}
		position = cursor.Position
	} else {
		if err := readTagSnapshot(ctx, tagLatestKey(window, scope), &snap); err != nil || time.Since(snap.AsOf) > 5*time.Minute {
			built, err := buildTagSnapshot(ctx, window, scope)
			if errors.Is(err, ErrTrendBusy) {
				built, err = awaitTagSnapshot(ctx, window, scope)
			}
			if err != nil {
				return nil, err
			}
			snap = *built
		}
	}
	if snap.Window != window || snap.Scope != scope || position > len(snap.Keys) || time.Since(snap.AsOf) > 10*time.Minute {
		return nil, ErrTagCursor
	}
	end := min(position+limit, len(snap.Keys))
	selected := snap.Keys[position:end]
	// A partial snapshot ranked the newest public roots across all tags. Do
	// not expand that sample with older roots merely because this page asks
	// for fewer tags: otherwise counts and eligibility depend on page size.
	keys := selected
	if snap.Partial && len(selected) > 0 {
		keys = nil
	}
	rows, partial, err := AggregateTagTrendsPartial(ctx, db.GetDB(), viewer, window, scope, snap.AsOf, keys)
	if err != nil {
		return nil, err
	}
	partial = partial || snap.Partial
	byKey := map[string]TagTrend{}
	for _, row := range rows {
		byKey[row.key] = row
	}
	page := &TagTrendPage{Items: []TagTrend{}, AsOf: snap.AsOf, CheckedAt: time.Now().UTC(), Window: window, Scope: scope, Kind: snap.Kind, Version: TagTrendVersion + ":" + snap.Generation, CoverageComplete: !partial}
	for _, key := range selected {
		row, exists := byKey[key]
		if !exists || snap.Kind == "trending" && row.AuthorsCount < TagTrendMinAuthors {
			continue
		}
		page.Items = append(page.Items, row)
	}
	if end < len(snap.Keys) {
		next, err := signTagCursor(tagTrendCursor{Generation: snap.Generation, Window: window, Scope: scope, Viewer: viewerID, Position: end, Expires: snap.AsOf.Add(10 * time.Minute).Unix()})
		if err != nil {
			return nil, err
		}
		page.NextCursor = &next
	}
	return page, nil
}

// awaitTagSnapshot waits briefly for a concurrent builder (another request or
// the background refresh) to publish the bucket instead of failing at once.
func awaitTagSnapshot(ctx context.Context, window, scope string) (*tagTrendSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var snap tagTrendSnapshot
		if err := readTagSnapshot(ctx, tagLatestKey(window, scope), &snap); err == nil && time.Since(snap.AsOf) <= 5*time.Minute {
			return &snap, nil
		}
		select {
		case <-ctx.Done():
			return nil, ErrTrendBusy
		case <-ticker.C:
		}
	}
}
