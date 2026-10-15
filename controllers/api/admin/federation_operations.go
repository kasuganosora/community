package admin

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

func operationsAuthorized(c *gin.Context, permission uint64) bool {
	a := controllers.GetAccount(c)
	if a == nil || a.UserId == 0 {
		c.AbortWithStatusJSON(401, gin.H{"error": "authentication required"})
		return false
	}
	var u models.User
	if db.GetDB().WithContext(c.Request.Context()).First(&u, a.UserId).Error != nil || u.IsBan || u.Status != models.UserStatusNormal {
		c.AbortWithStatusJSON(403, gin.H{"error": "account unavailable"})
		return false
	}
	if u.IsAdmin {
		return true
	}
	p, e := models.GetUserPermissions(db.GetDB(), u.Id)
	if e != nil || (p&permission == 0 && p&models.PermissionAdministrator == 0) {
		c.AbortWithStatusJSON(403, gin.H{"error": "permission required"})
		return false
	}
	return true
}
func operationsPage(c *gin.Context) (int, int, bool) {
	page, limit := 1, 30
	var e error
	if v := c.Query("page"); v != "" {
		page, e = strconv.Atoi(v)
		if e != nil || page < 1 || page > 100000 {
			c.JSON(422, gin.H{"error": "invalid page"})
			return 0, 0, false
		}
	}
	if v := c.Query("limit"); v != "" {
		limit, e = strconv.Atoi(v)
		if e != nil || limit < 1 || limit > 50 {
			c.JSON(422, gin.H{"error": "limit must be 1–50"})
			return 0, 0, false
		}
	}
	return page, limit, true
}
func operationsID(c *gin.Context) (uint64, bool) {
	id, e := strconv.ParseUint(c.Param("id"), 10, 64)
	if e != nil || id == 0 {
		c.JSON(404, gin.H{"error": "not found"})
		return 0, false
	}
	return id, true
}
func operationError(c *gin.Context, e error) bool {
	if e == nil {
		return false
	}
	c.JSON(500, gin.H{"error": "operation could not be completed"})
	return true
}

var instanceSorts = map[string]string{
	"pubsub_desc":        "pubsub DESC, t.domain",
	"pubsub_asc":         "pubsub ASC, t.domain",
	"accounts_desc":      "t.accounts DESC, t.domain",
	"accounts_asc":       "t.accounts ASC, t.domain",
	"last_activity_desc": "CASE WHEN t.last_activity IS NULL THEN 1 ELSE 0 END, t.last_activity DESC, t.domain",
	"last_activity_asc":  "CASE WHEN t.last_activity IS NULL THEN 1 ELSE 0 END, t.last_activity ASC, t.domain",
	"first_seen_desc":    "t.first_seen DESC, t.domain",
	"first_seen_asc":     "t.first_seen ASC, t.domain",
	"domain":             "t.domain",
}

// FederationInstances lists remote domains with account/follow aggregates
// and the cached nodeinfo metadata. It only reads the cache; metadata is
// filled by the background worker. Filters: q (domain substring), status
// (federating|subscribing|publishing|failing), sort (see instanceSorts).
func FederationInstances(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionManageFederation) {
		return
	}
	page, limit, ok := operationsPage(c)
	if !ok {
		return
	}
	order, ok := instanceSorts[c.DefaultQuery("sort", "domain")]
	if !ok {
		c.JSON(422, gin.H{"error": "invalid sort"})
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	inner := conn.Table("accounts AS a").Where("a.domain IS NOT NULL AND a.domain <> ''")
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		if len(search) > 255 {
			c.JSON(422, gin.H{"error": "search too long"})
			return
		}
		inner = inner.Where("a.domain "+dialect.ILike(inner)+" ?", "%"+search+"%")
	}
	followed := models.FollowStatusFollowed
	inner = inner.Select(`a.domain AS domain, COUNT(*) AS accounts, SUM(CASE WHEN a.is_ban = ? THEN 1 ELSE 0 END) AS suspended_accounts, MAX(a.last_activity_at) AS last_activity, MIN(a.created_at) AS first_seen,
(SELECT COUNT(*) FROM follows f JOIN accounts ra ON ra.id = f.follow_id JOIN accounts la ON la.id = f.account_id WHERE f.status = ? AND ra.domain = a.domain AND la.domain IS NULL) AS following,
(SELECT COUNT(*) FROM follows f JOIN accounts ra ON ra.id = f.account_id JOIN accounts la ON la.id = f.follow_id WHERE f.status = ? AND ra.domain = a.domain AND la.domain IS NULL) AS followers`, true, followed, followed).Group("a.domain")
	q := conn.Table("(?) AS t", inner).Joins("LEFT JOIN instance_metadata m ON m.domain = t.domain")
	switch c.Query("status") {
	case "":
	case "federating":
		q = q.Where("t.following > 0 OR t.followers > 0")
	case "subscribing":
		q = q.Where("t.following > 0")
	case "publishing":
		q = q.Where("t.followers > 0")
	case "failing":
		q = q.Where("m.fail_count > 0")
	default:
		c.JSON(422, gin.H{"error": "invalid status"})
		return
	}
	var total int64
	if operationError(c, q.Session(&gorm.Session{}).Count(&total).Error) {
		return
	}
	type instance struct {
		Domain            string   `json:"domain"`
		Name              string   `json:"name"`
		Software          string   `json:"software"`
		Version           string   `json:"version"`
		IconURL           string   `json:"icon_url"`
		Accounts          int64    `json:"accounts"`
		SuspendedAccounts int64    `json:"suspended_accounts"`
		LastActivity      flexTime `json:"last_activity"`
		FirstSeen         flexTime `json:"first_seen"`
		Following         int64    `json:"following"`
		Followers         int64    `json:"followers"`
		FailCount         int      `json:"-"`
		FetchedAt         flexTime `json:"metadata_fetched_at"`
		FailedAt          flexTime `json:"metadata_failed_at"`
		Status            string   `json:"status" gorm:"-"`
	}
	var rows []instance
	if operationError(c, q.Select(`t.*, t.following + t.followers AS pubsub, COALESCE(m.name, '') AS name, COALESCE(m.software, '') AS software, COALESCE(m.version, '') AS version, COALESCE(m.icon_url, '') AS icon_url, COALESCE(m.fail_count, 0) AS fail_count, m.fetched_at AS fetched_at, m.failed_at AS failed_at`).Order(order).Offset((page-1)*limit).Limit(limit).Scan(&rows).Error) {
		return
	}
	for i := range rows {
		r := &rows[i]
		r.IconURL = mediaproxy.URL(mediaproxy.KindAvatar, r.IconURL)
		switch {
		case r.FailCount > 0:
			r.Status = "failing"
		case r.Following > 0 && r.Followers > 0:
			r.Status = "federating"
		case r.Following > 0:
			r.Status = "subscribing"
		case r.Followers > 0:
			r.Status = "publishing"
		default:
			r.Status = "idle"
		}
	}
	if rows == nil {
		rows = []instance{}
	}
	c.JSON(200, gin.H{"items": rows, "total": total, "page": page, "limit": limit, "domain_blocking_supported": false})
}

const instanceActivityDays = 14

type activityCacheEntry struct {
	values  []int64
	expires time.Time
}

var (
	instanceActivityMu    sync.Mutex
	instanceActivityCache = map[string]activityCacheEntry{}
)

// FederationInstanceActivity returns, per requested domain, the number of
// statuses received from that domain on each of the last 14 days (oldest
// first). Results are cached in memory for 10 minutes per domain.
func FederationInstanceActivity(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionManageFederation) {
		return
	}
	var domains []string
	for _, d := range strings.Split(c.Query("domains"), ",") {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" {
			continue
		}
		if len(d) > 255 || !queueHostPattern.MatchString(d) {
			c.JSON(422, gin.H{"error": "invalid domain"})
			return
		}
		domains = append(domains, d)
	}
	if len(domains) == 0 || len(domains) > 50 {
		c.JSON(422, gin.H{"error": "domains must list 1–50 hosts"})
		return
	}
	now := time.Now()
	out := map[string][]int64{}
	var missing []string
	instanceActivityMu.Lock()
	for _, d := range domains {
		if e, ok := instanceActivityCache[d]; ok && now.Before(e.expires) {
			out[d] = e.values
		} else {
			missing = append(missing, d)
		}
	}
	instanceActivityMu.Unlock()
	if len(missing) > 0 {
		day := now.Truncate(24 * time.Hour)
		start := day.AddDate(0, 0, -(instanceActivityDays - 1))
		sel := "a.domain AS domain"
		args := []any{}
		for i := 0; i < instanceActivityDays; i++ {
			sel += fmt.Sprintf(", SUM(CASE WHEN s.created_at >= ? AND s.created_at < ? THEN 1 ELSE 0 END) AS d%d", i)
			args = append(args, start.AddDate(0, 0, i), start.AddDate(0, 0, i+1))
		}
		var rows []map[string]any
		if operationError(c, db.GetDB().WithContext(c.Request.Context()).Table("statuses AS s").Joins("JOIN accounts a ON a.id = s.account_id").
			Select(sel, args...).Where("a.domain IN ? AND s.created_at >= ?", missing, start).Group("a.domain").Find(&rows).Error) {
			return
		}
		got := map[string][]int64{}
		for _, r := range rows {
			d := strings.ToLower(fmt.Sprint(r["domain"]))
			v := make([]int64, instanceActivityDays)
			for i := range v {
				v[i] = toInt64(r[fmt.Sprintf("d%d", i)])
			}
			got[d] = v
		}
		instanceActivityMu.Lock()
		if len(instanceActivityCache) > 4096 {
			instanceActivityCache = map[string]activityCacheEntry{}
		}
		for _, d := range missing {
			v, ok := got[d]
			if !ok {
				v = make([]int64, instanceActivityDays)
			}
			instanceActivityCache[d] = activityCacheEntry{values: v, expires: now.Add(10 * time.Minute)}
			out[d] = v
		}
		instanceActivityMu.Unlock()
	}
	c.JSON(200, gin.H{"days": instanceActivityDays, "items": out})
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case float64:
		return int64(n)
	case []byte:
		i, _ := strconv.ParseInt(string(n), 10, 64)
		return i
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	}
	return 0
}

var operationURL = regexp.MustCompile(`https?://[^\s]+`)

func safeJobError(value string) string {
	value = operationURL.ReplaceAllString(value, "[remote URL]")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	if len(value) > 400 {
		value = value[:400] + "…"
	}
	return value
}
func safeInbox(raw string) string {
	u, e := url.Parse(raw)
	if e != nil {
		return ""
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// queueStateScope adds the derived "delayed" (pending, scheduled in the
// future) and "waiting" (pending, due now) pseudo-states.
func queueStateScope(q *gorm.DB, stateCol, nextCol, state string) *gorm.DB {
	switch state {
	case "":
		return q
	case "delayed":
		return q.Where(stateCol+" = ? AND "+nextCol+" > ?", "pending", time.Now())
	case "waiting":
		return q.Where(stateCol+" = ? AND ("+nextCol+" IS NULL OR "+nextCol+" <= ?)", "pending", time.Now())
	}
	return q.Where(stateCol+" = ?", state)
}
func FederationJobs(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionDevops) {
		return
	}
	page, limit, ok := operationsPage(c)
	if !ok {
		return
	}
	kind := c.DefaultQuery("kind", "delivery")
	state := c.Query("state")
	conn := db.GetDB().WithContext(c.Request.Context())
	items := []gin.H{}
	var total int64
	switch kind {
	case "matrix-direct":
		DirectDeliveryJobs(c)
		return
	case "feed-event", "feed-batch":
		feedJobs(c, conn, kind, state, page, limit)
		return
	case "delivery":
		q := queueStateScope(conn.Model(&activitypub.Delivery{}), "status", "next_retry_at", state)
		if search := strings.TrimSpace(c.Query("q")); search != "" {
			if len(search) > 255 {
				c.JSON(422, gin.H{"error": "search too long"})
				return
			}
			q = q.Where("(target_inbox LIKE ? OR activity_id LIKE ?)", "%"+search+"%", "%"+search+"%")
		}
		if host := strings.ToLower(strings.TrimSpace(c.Query("host"))); host != "" {
			if !queueHostPattern.MatchString(host) {
				c.JSON(422, gin.H{"error": "invalid host"})
				return
			}
			q = hostScope(q, host)
		}
		if operationError(c, q.Count(&total).Error) {
			return
		}
		var rows []activitypub.Delivery
		if operationError(c, q.Order("id DESC").Offset((page-1)*limit).Limit(limit).Find(&rows).Error) {
			return
		}
		for _, r := range rows {
			items = append(items, gin.H{"id": fmt.Sprint(r.ID), "kind": kind, "state": r.Status, "activity_id": r.ActivityID, "activity_type": activityType(r.Payload), "destination": safeInbox(r.TargetInbox), "attempts": r.Retries, "last_error": safeJobError(r.LastError), "next_attempt_at": r.NextRetryAt, "created_at": r.CreatedAt})
		}
	case "outbox":
		q := queueStateScope(conn.Model(&models.FederationOutbox{}), "state", "next_attempt_at", state)
		if search := strings.TrimSpace(c.Query("q")); search != "" {
			if len(search) > 64 {
				c.JSON(422, gin.H{"error": "search too long"})
				return
			}
			q = q.Where("kind LIKE ?", "%"+search+"%")
		}
		if operationError(c, q.Count(&total).Error) {
			return
		}
		var rows []models.FederationOutbox
		if operationError(c, q.Order("id DESC").Offset((page-1)*limit).Limit(limit).Find(&rows).Error) {
			return
		}
		for _, r := range rows {
			items = append(items, gin.H{"id": fmt.Sprint(r.ID), "kind": kind, "state": r.State, "event": r.Kind, "object_id": fmt.Sprint(r.ObjectID), "account_id": fmt.Sprint(r.AccountID), "attempts": r.Attempts, "last_error": safeJobError(r.LastError), "next_attempt_at": r.NextAttemptAt, "created_at": r.CreatedAt})
		}
	default:
		c.JSON(422, gin.H{"error": "invalid queue kind"})
		return
	}
	c.JSON(200, gin.H{"items": items, "total": total, "page": page, "limit": limit})
}
func FederationJobAction(c *gin.Context) {
	if !operationsAuthorized(c, models.PermissionDevops) {
		return
	}
	id, ok := operationsID(c)
	if !ok {
		return
	}
	var body struct {
		Action  string `json:"action"`
		Confirm bool   `json:"confirm"`
	}
	if c.ShouldBindJSON(&body) != nil || (body.Action != "retry" && body.Action != "cancel") || (body.Action == "cancel" && !body.Confirm) {
		c.JSON(422, gin.H{"error": "retry or confirmed cancellation required"})
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var q *gorm.DB
	updates := map[string]any{}
	switch c.Param("kind") {
	case "feed-event", "feed-batch":
		feedJobAction(c, conn, c.Param("kind"), id, body.Action)
		return
	case "delivery":
		q = conn.Model(&activitypub.Delivery{}).Where("id = ? AND status IN ?", id, []string{"failed", "pending"})
		updates["status"] = "pending"
		updates["next_retry_at"] = time.Now()
		if body.Action == "cancel" {
			updates["status"] = "cancelled"
		}
	case "outbox":
		q = conn.Model(&models.FederationOutbox{}).Where("id = ? AND state IN ?", id, []string{"failed", "pending"})
		updates["state"] = "pending"
		updates["next_attempt_at"] = time.Now()
		if body.Action == "cancel" {
			updates["state"] = "cancelled"
		}
	default:
		c.JSON(422, gin.H{"error": "invalid queue kind"})
		return
	}
	r := q.Updates(updates)
	if operationError(c, r.Error) {
		return
	}
	if r.RowsAffected != 1 {
		c.JSON(409, gin.H{"error": "job is running, completed or changed; refresh before retrying"})
		return
	}
	c.JSON(200, gin.H{"updated": true})
}

// activityType exposes only the top-level ActivityStreams type, never the body.
func activityType(payload string) string {
	var v struct {
		Type any `json:"type"`
	}
	if json.Unmarshal([]byte(payload), &v) != nil {
		return ""
	}
	if t, ok := v.Type.(string); ok && len(t) <= 64 {
		return t
	}
	return ""
}

// flexTime scans timestamps that drivers may return as text through derived
// tables (SQLite loses the column type) and marshals null when unset.
type flexTime struct{ t *time.Time }

func (f *flexTime) Scan(v any) error {
	f.t = nil
	switch x := v.(type) {
	case time.Time:
		f.t = &x
	case []byte:
		return f.parse(string(x))
	case string:
		return f.parse(x)
	}
	return nil
}

func (f *flexTime) parse(s string) error {
	for _, layout := range []string{"2006-01-02 15:04:05.999999999-07:00", time.RFC3339Nano, "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05"} {
		if t, e := time.Parse(layout, s); e == nil {
			f.t = &t
			return nil
		}
	}
	return nil
}

func (f flexTime) MarshalJSON() ([]byte, error) {
	if f.t == nil {
		return []byte("null"), nil
	}
	return json.Marshal(f.t)
}

func (f flexTime) Value() (driver.Value, error) {
	if f.t == nil {
		return nil, nil
	}
	return *f.t, nil
}
