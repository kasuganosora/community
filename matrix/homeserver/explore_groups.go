package homeserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ===== Explore → Groups =====
//
// The Explore "Groups" tab lists public, joinable web groups: local groups
// published in the room directory and remote groups this server already knows.
// Ranking is a "hot" score computed by a background job into
// matrix_group_hot_scores; the listing only reads that table through its
// ordered index and never aggregates events on page load.

// Hot score tuning. Every weight lives here so ranking can be retuned in one place.
const (
	// Activity window and decay: each event counts weight * 0.5^(age/half-life).
	groupHotWindow   = 7 * 24 * time.Hour
	groupHotHalfLife = 2 * 24 * time.Hour
	// Messages; one sender contributes at most groupHotPostsPerSenderCap decayed posts,
	// so a single spammer cannot dominate.
	groupHotPostWeight        = 1.0
	groupHotPostsPerSenderCap = 10.0
	// Distinct posters weigh more than raw posts (decayed by each poster's latest post).
	groupHotPosterWeight = 4.0
	// Distinct members that joined inside the window.
	groupHotJoinWeight = 2.0
	// New groups (created / first indexed here) get a boost that fades linearly to 0.
	groupHotNewWindow = 3 * 24 * time.Hour
	groupHotNewBoost  = 6.0
	// Small size term: weight * ln(member_count + 1).
	groupHotMemberWeight = 0.5
	// Stored as an integer so keyset pagination compares exact values.
	groupHotScoreScale = 1000.0

	groupHotInterval      = 12 * time.Minute
	groupHotRoomBatch     = 100
	groupHotEventBatchCap = 50000
)

// MatrixGroupHotScore is the precomputed Explore ranking row of one eligible group.
// Rows are rewritten by the job and removed when a group stops being eligible.
type MatrixGroupHotScore struct {
	RoomID      string    `gorm:"primaryKey;type:varchar(255)"`
	ScoreKey    int64     `gorm:"not null;default:0;index:idx_group_hot_order,priority:1"`
	LastPostTS  int64     `gorm:"not null;default:0;index:idx_group_hot_order,priority:2"`
	CreatedTS   int64     `gorm:"not null;default:0;index:idx_group_hot_order,priority:3"`
	Server      string    `gorm:"type:varchar(255);not null;default:'';index"`
	IsLocal     bool      `gorm:"not null;default:false"`
	MemberCount int64     `gorm:"not null;default:0"`
	ComputedAt  time.Time `gorm:"not null;index"`
}

func (MatrixGroupHotScore) TableName() string { return "matrix_group_hot_scores" }

type groupHotActivity struct {
	posts     map[string]float64 // sender -> decayed post sum
	posterAge map[string]float64 // sender -> decay of latest post
	joins     map[string]float64 // member -> decay of latest join
	lastPost  int64
}

// groupHotScore combines one group's activity into the stored score. It is the
// single ranking expression; change weights above, not here.
func groupHotScore(a *groupHotActivity, members int64, created time.Time, now time.Time) float64 {
	score := 0.0
	if a != nil {
		for _, sum := range a.posts {
			score += groupHotPostWeight * math.Min(sum, groupHotPostsPerSenderCap)
		}
		for _, d := range a.posterAge {
			score += groupHotPosterWeight * d
		}
		for _, d := range a.joins {
			score += groupHotJoinWeight * d
		}
	}
	if age := now.Sub(created); !created.IsZero() && age >= 0 && age < groupHotNewWindow {
		score += groupHotNewBoost * (1 - float64(age)/float64(groupHotNewWindow))
	}
	return score + groupHotMemberWeight*math.Log(float64(max(members, 0))+1)
}

func groupHotDecay(ts int64, now time.Time) float64 {
	age := now.Sub(time.UnixMilli(ts))
	if age < 0 {
		age = 0
	}
	return math.Pow(0.5, float64(age)/float64(groupHotHalfLife))
}

func matrixIDServer(id string) string {
	if i := strings.IndexByte(id, ':'); i >= 0 {
		return strings.ToLower(id[i+1:])
	}
	return ""
}

// normalizeServerHost lower-cases a Matrix server name and strips any port,
// so "C.Hime.At:8448" and "c.hime.at" compare equal.
func normalizeServerHost(name string) string {
	name = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if strings.HasPrefix(name, "[") { // IPv6 literal
		if i := strings.IndexByte(name, ']'); i > 0 {
			return name[:i+1]
		}
		return name
	}
	if i := strings.LastIndexByte(name, ':'); i >= 0 {
		return name[:i]
	}
	return name
}

func groupHomeServer(room *MatrixRoom) string {
	if s := matrixIDServer(room.Creator); s != "" {
		return normalizeServerHost(s)
	}
	return normalizeServerHost(matrixIDServer(room.RoomID))
}

// exploreGroupIsLocal decides whether a group belongs to this server, using the
// effective homeserver name (hs.Config.ServerName, resolved at startup from
// SERVER_NAME, the instance domain setting or the default). Groups created by
// the web UI (org.community.web_group state) and groups with an unknown creator
// count as local, so they are only listed when published to the directory.
func exploreGroupIsLocal(room *MatrixRoom, localServer string, webCreated bool) bool {
	if webCreated || strings.TrimSpace(room.Creator) == "" {
		return true
	}
	return groupHomeServer(room) == normalizeServerHost(localServer)
}

// exploreGroupListable applies the listing rule after eligibleExploreGroups:
// local groups must be published to the room directory; known remote groups
// only need the public join rule checked by the query.
func exploreGroupListable(room *MatrixRoom, local bool) bool {
	return !local || room.Visibility == "public"
}

func webCreatedRooms(conn *gorm.DB, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []string
	if err := conn.Model(&MatrixRoomState{}).Where("room_id IN ? AND event_type = ?", ids, "org.community.web_group").Distinct().Pluck("room_id", &rows).Error; err != nil {
		return nil, err
	}
	for _, id := range rows {
		out[id] = true
	}
	return out, nil
}

// eligibleExploreGroups selects public-join, plain web groups. DMs, bridged
// conversations, spaces and encrypted rooms are never listed. The local/remote
// directory rule is applied in Go (exploreGroupIsLocal/exploreGroupListable).
func eligibleExploreGroups(conn *gorm.DB) *gorm.DB {
	return webGroupRoomQuery(conn).
		Where("(room_type IS NULL OR room_type = '')").
		Where("join_rule = ? AND is_encrypted = ?", "public", false)
}

// RefreshGroupHotScores recomputes all Explore group scores. Event reads are
// indexed range scans on (room_id, origin_server_ts) limited to the window,
// done in room batches; writes are batched upserts.
func RefreshGroupHotScores(ctx context.Context, hs *HomeServer, now time.Time) error {
	conn := db.GetDB().WithContext(ctx)
	local := hs.Config.ServerName
	suspend, _, err := models.DomainSeverities(conn)
	if err != nil {
		return err
	}
	since := now.Add(-groupHotWindow).UnixMilli()
	runStart := now.UTC().Truncate(time.Second)
	after := ""
	for {
		var rooms []MatrixRoom
		q := eligibleExploreGroups(conn).Order("room_id ASC").Limit(groupHotRoomBatch)
		if after != "" {
			q = q.Where("room_id > ?", after)
		}
		if err := q.Find(&rooms).Error; err != nil {
			return err
		}
		if len(rooms) == 0 {
			break
		}
		after = rooms[len(rooms)-1].RoomID
		ids := make([]string, 0, len(rooms))
		for _, r := range rooms {
			ids = append(ids, r.RoomID)
		}
		web, err := webCreatedRooms(conn, ids)
		if err != nil {
			return err
		}
		keep := make([]MatrixRoom, 0, len(rooms))
		isLocal := map[string]bool{}
		for _, r := range rooms {
			l := exploreGroupIsLocal(&r, local, web[r.RoomID])
			if !exploreGroupListable(&r, l) || (!l && (groupHomeServer(&r) == "" || suspend.Contains(groupHomeServer(&r)))) {
				continue
			}
			isLocal[r.RoomID] = l
			keep = append(keep, r)
		}
		if err := refreshGroupHotBatch(conn, local, keep, isLocal, since, now, runStart); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return conn.Where("computed_at < ?", runStart).Delete(&MatrixGroupHotScore{}).Error
}

func refreshGroupHotBatch(conn *gorm.DB, local string, rooms []MatrixRoom, isLocal map[string]bool, since int64, now time.Time, runStart time.Time) error {
	if len(rooms) == 0 {
		return nil
	}
	ids := make([]string, 0, len(rooms))
	for _, r := range rooms {
		ids = append(ids, r.RoomID)
	}
	// Suspended local creators hide their groups.
	banned := map[string]bool{}
	var locals []string
	for _, r := range rooms {
		if isLocal[r.RoomID] && r.Creator != "" {
			if name, _, ok := ParseMatrixUserID(r.Creator); ok {
				locals = append(locals, name)
			}
		}
	}
	if len(locals) > 0 {
		var names []string
		sub := conn.Model(&models.User{}).Select("id").Where("is_ban = ? OR status <> ?", true, models.UserStatusNormal)
		if err := conn.Model(&models.Account{}).Where("domain IS NULL AND username IN ? AND (is_ban = ? OR user_id IN (?))", locals, true, sub).Pluck("username", &names).Error; err != nil {
			return err
		}
		for _, n := range names {
			banned[strings.ToLower(n)] = true
		}
	}
	type countRow struct {
		RoomID string
		N      int64
	}
	var counts []countRow
	if err := conn.Model(&MatrixRoomMember{}).Select("room_id, COUNT(*) AS n").Where("room_id IN ? AND membership = ?", ids, MembershipJoin).Group("room_id").Scan(&counts).Error; err != nil {
		return err
	}
	members := map[string]int64{}
	for _, c := range counts {
		members[c.RoomID] = c.N
	}
	var prev []MatrixGroupHotScore
	if err := conn.Select("room_id, last_post_ts").Where("room_id IN ?", ids).Find(&prev).Error; err != nil {
		return err
	}
	lastKnown := map[string]int64{}
	for _, p := range prev {
		lastKnown[p.RoomID] = p.LastPostTS
	}
	type eventRow struct {
		RoomID         string
		EventType      string
		Sender         string
		StateKey       *string
		OriginServerTS int64
		Content        map[string]any `gorm:"serializer:json"`
	}
	var events []eventRow
	if err := conn.Model(&MatrixEvent{}).Select("room_id, event_type, sender, state_key, origin_server_ts, content").
		Where("room_id IN ? AND origin_server_ts >= ? AND origin_server_ts <= ?", ids, since, now.Add(time.Hour).UnixMilli()).
		Where("event_type IN ?", []string{"m.room.message", "m.room.encrypted", "m.room.member"}).
		Limit(groupHotEventBatchCap).Scan(&events).Error; err != nil {
		return err
	}
	acts := map[string]*groupHotActivity{}
	for _, e := range events {
		a := acts[e.RoomID]
		if a == nil {
			a = &groupHotActivity{posts: map[string]float64{}, posterAge: map[string]float64{}, joins: map[string]float64{}}
			acts[e.RoomID] = a
		}
		d := groupHotDecay(e.OriginServerTS, now)
		if e.EventType == "m.room.member" {
			if e.StateKey != nil && e.Content["membership"] == "join" && d > a.joins[*e.StateKey] {
				a.joins[*e.StateKey] = d
			}
			continue
		}
		a.posts[e.Sender] += d
		if d > a.posterAge[e.Sender] {
			a.posterAge[e.Sender] = d
		}
		if e.OriginServerTS > a.lastPost {
			a.lastPost = e.OriginServerTS
		}
	}
	rows := make([]MatrixGroupHotScore, 0, len(rooms))
	for _, r := range rooms {
		if name, _, ok := ParseMatrixUserID(r.Creator); ok && isLocal[r.RoomID] && banned[strings.ToLower(name)] {
			continue
		}
		created := time.Time{}
		if r.CreatedAt != nil {
			created = *r.CreatedAt
		}
		a := acts[r.RoomID]
		last := lastKnown[r.RoomID]
		if a != nil && a.lastPost > last {
			last = a.lastPost
		}
		if last == 0 && r.LastEventTS != nil {
			last = *r.LastEventTS // first run: best known activity time
		}
		server := groupHomeServer(&r)
		if isLocal[r.RoomID] {
			server = normalizeServerHost(local)
		}
		rows = append(rows, MatrixGroupHotScore{
			RoomID: r.RoomID, ScoreKey: int64(math.Round(groupHotScore(a, members[r.RoomID], created, now) * groupHotScoreScale)),
			LastPostTS: last, CreatedTS: created.UnixMilli(), Server: server, IsLocal: isLocal[r.RoomID],
			MemberCount: members[r.RoomID], ComputedAt: runStart,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return conn.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "room_id"}}, UpdateAll: true}).Create(&rows).Error
}

var groupHotOnce sync.Once

func startGroupHotScores() {
	groupHotOnce.Do(func() {
		go func() {
			run := func() {
				hs := GetHomeServer()
				if hs == nil || db.GetDB() == nil {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				if err := RefreshGroupHotScores(ctx, hs, time.Now()); err != nil {
					log.Warn("Explore group score refresh failed: %v", err)
				}
			}
			time.Sleep(30 * time.Second)
			run()
			for range time.NewTicker(groupHotInterval).C {
				run()
			}
		}()
	})
}

// ----- listing -----

type exploreGroupCursor struct {
	Score, Last, Created int64
	Room                 string
}

func encodeExploreCursor(c exploreGroupCursor) string {
	return base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "%d|%d|%d|%s", c.Score, c.Last, c.Created, c.Room))
}

func decodeExploreCursor(raw string) (exploreGroupCursor, error) {
	var c exploreGroupCursor
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return c, err
	}
	parts := strings.SplitN(string(b), "|", 4)
	if len(parts) != 4 || !validGroupRoomID(parts[3]) {
		return c, errors.New("bad cursor")
	}
	var e1, e2, e3 error
	c.Score, e1 = strconv.ParseInt(parts[0], 10, 64)
	c.Last, e2 = strconv.ParseInt(parts[1], 10, 64)
	c.Created, e3 = strconv.ParseInt(parts[2], 10, 64)
	c.Room = parts[3]
	return c, errors.Join(e1, e2, e3)
}

func exploreFailure(c *gin.Context, status int, code, msg string) {
	c.JSON(status, gin.H{"error": msg, "code": code})
}

// HandleExploreGroups serves GET /api/v1/explore/groups. Visitors may read it
// unless the instance requires sign-in (enforced by the route's visitor guard).
func HandleExploreGroups(c *gin.Context) {
	hs := GetHomeServer()
	if hs == nil || hs.Config == nil || !hs.Config.Enabled {
		c.JSON(200, []any{})
		return
	}
	limit := 20
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			exploreFailure(c, 400, "EXPLORE_GROUPS_INVALID_LIMIT", "Invalid page size")
			return
		}
		limit = min(n, 40)
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	q := conn.Model(&MatrixGroupHotScore{})
	if raw := c.Query("cursor"); raw != "" {
		cur, err := decodeExploreCursor(raw)
		if err != nil {
			exploreFailure(c, 400, "EXPLORE_GROUPS_INVALID_CURSOR", "Invalid page cursor")
			return
		}
		q = q.Where("(score_key < ?) OR (score_key = ? AND last_post_ts < ?) OR (score_key = ? AND last_post_ts = ? AND created_ts < ?) OR (score_key = ? AND last_post_ts = ? AND created_ts = ? AND room_id > ?)",
			cur.Score, cur.Score, cur.Last, cur.Score, cur.Last, cur.Created, cur.Score, cur.Last, cur.Created, cur.Room)
	}
	if v := strings.ToLower(c.Query("local")); v == "true" || v == "1" {
		q = q.Where("is_local = ?", true)
	}
	suspend, _, err := models.DomainSeverities(conn)
	if err != nil {
		exploreFailure(c, 500, "EXPLORE_GROUPS_UNAVAILABLE", "Groups are temporarily unavailable")
		return
	}
	var viewer *models.Account
	if raw, ok := c.Get("account"); ok {
		viewer, _ = raw.(*models.Account)
	}
	userBlocked := models.HostSet{}
	if viewer != nil && viewer.Id != 0 {
		var domains []string
		conn.Model(&models.Block{}).Where("account_id = ? AND type = ?", viewer.Id, models.BlockTypeDomain).Pluck("target_domain", &domains)
		for _, d := range domains {
			userBlocked[strings.ToLower(strings.TrimSpace(d))] = true
		}
	}
	// Hidden servers are rare: filter in Go while over-fetching a few pages, so
	// the query stays a pure index-ordered range scan.
	order := "score_key DESC, last_post_ts DESC, created_ts DESC, room_id ASC"
	picked := make([]MatrixGroupHotScore, 0, limit)
	more := false
	for round := 0; round < 5 && len(picked) <= limit; round++ {
		var rows []MatrixGroupHotScore
		if err := q.Session(&gorm.Session{}).Order(order).Limit(limit + 1).Find(&rows).Error; err != nil {
			exploreFailure(c, 500, "EXPLORE_GROUPS_UNAVAILABLE", "Groups are temporarily unavailable")
			return
		}
		for _, r := range rows {
			if !r.IsLocal && (suspend.Contains(r.Server) || userBlocked.Contains(r.Server)) {
				continue
			}
			picked = append(picked, r)
		}
		if len(rows) <= limit {
			break
		}
		last := rows[len(rows)-1]
		q = q.Where("(score_key < ?) OR (score_key = ? AND last_post_ts < ?) OR (score_key = ? AND last_post_ts = ? AND created_ts < ?) OR (score_key = ? AND last_post_ts = ? AND created_ts = ? AND room_id >= ?)",
			last.ScoreKey, last.ScoreKey, last.LastPostTS, last.ScoreKey, last.LastPostTS, last.CreatedTS, last.ScoreKey, last.LastPostTS, last.CreatedTS, last.RoomID)
		more = true
	}
	// Dedup (the >= re-reads the boundary row).
	seen := map[string]bool{}
	uniq := picked[:0]
	for _, r := range picked {
		if !seen[r.RoomID] {
			seen[r.RoomID] = true
			uniq = append(uniq, r)
		}
	}
	picked = uniq
	if len(picked) > limit {
		picked, more = picked[:limit], true
	} else if len(picked) < limit {
		more = false
	}
	out, err := exploreGroupViews(c, hs, conn, picked, viewer)
	if err != nil {
		exploreFailure(c, 500, "EXPLORE_GROUPS_UNAVAILABLE", "Groups are temporarily unavailable")
		return
	}
	if more && len(picked) > 0 {
		l := picked[len(picked)-1]
		next := url.Values{}
		next.Set("limit", strconv.Itoa(limit))
		next.Set("cursor", encodeExploreCursor(exploreGroupCursor{l.ScoreKey, l.LastPostTS, l.CreatedTS, l.RoomID}))
		if c.Query("local") != "" {
			next.Set("local", c.Query("local"))
		}
		c.Header("Link", fmt.Sprintf("<%s?%s>; rel=\"next\"", requestBase(c)+"/api/v1/explore/groups", next.Encode()))
	}
	c.Header("Cache-Control", "private, max-age=30")
	c.JSON(200, out)
}

func requestBase(c *gin.Context) string {
	scheme := "https"
	if c.Request.TLS == nil && c.GetHeader("X-Forwarded-Proto") != "https" {
		if h := c.Request.Host; strings.HasPrefix(h, "localhost") || strings.HasPrefix(h, "127.") {
			scheme = "http"
		}
	}
	return scheme + "://" + c.Request.Host
}

func exploreGroupViews(c *gin.Context, hs *HomeServer, conn *gorm.DB, rows []MatrixGroupHotScore, viewer *models.Account) ([]gin.H, error) {
	out := make([]gin.H, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.RoomID)
	}
	var rooms []MatrixRoom
	if err := eligibleExploreGroups(conn).Where("room_id IN ?", ids).Find(&rooms).Error; err != nil {
		return nil, err
	}
	byID := map[string]*MatrixRoom{}
	for i := range rooms {
		byID[rooms[i].RoomID] = &rooms[i]
	}
	type aliasRow struct{ RoomID, RoomAlias string }
	var aliases []aliasRow
	_ = conn.Table("matrix_room_aliases").Select("room_id, room_alias").Where("room_id IN ?", ids).Order("room_alias ASC").Scan(&aliases).Error
	aliasOf := map[string]string{}
	for _, a := range aliases {
		if _, ok := aliasOf[a.RoomID]; !ok {
			aliasOf[a.RoomID] = a.RoomAlias
		}
	}
	membership := map[string]MembershipState{}
	if viewer != nil && viewer.Id != 0 && viewer.Domain == nil {
		var ms []MatrixRoomMember
		if err := conn.Where("room_id IN ? AND user_id = ?", ids, FormatMatrixUserID(viewer.Username, hs.Config.ServerName)).Find(&ms).Error; err != nil {
			return nil, err
		}
		for _, m := range ms {
			membership[m.RoomID] = m.Membership
		}
	}
	for _, r := range rows {
		room := byID[r.RoomID]
		if room == nil || !exploreGroupListable(room, r.IsLocal) {
			continue // became ineligible since the last refresh
		}
		name, topic := directoryString(room.Name), directoryString(room.Topic)
		alias := aliasOf[r.RoomID]
		if name == "" {
			name = alias
		}
		target := r.RoomID
		if validGroupAlias(alias) {
			target = alias
		}
		if len([]rune(topic)) > 280 {
			topic = string([]rune(topic)[:280]) + "…"
		}
		m := string(membership[r.RoomID])
		if m == "" {
			m = "none"
		}
		item := gin.H{
			"id": webGroupID(r.RoomID), "name": name, "topic": topic, "alias": alias, "domain": r.Server, "local": r.IsLocal,
			"member_count": r.MemberCount, "target": target, "via": []string{r.Server}, "membership": m, "join_rule": room.JoinRule,
			"last_post_at": nil, "created_at": time.UnixMilli(r.CreatedTS).UTC().Format(time.RFC3339),
		}
		if r.LastPostTS > 0 {
			item["last_post_at"] = time.UnixMilli(r.LastPostTS).UTC().Format(time.RFC3339)
		}
		if room.AvatarURL != nil && *room.AvatarURL != "" {
			item["avatar_url"] = "/api/v1/explore/groups/" + webGroupID(r.RoomID) + "/avatar"
		}
		out = append(out, item)
	}
	return out, nil
}

// HandleExploreGroupAvatar serves the avatar of a group currently listed on Explore.
func HandleExploreGroupAvatar(c *gin.Context) {
	hs := GetHomeServer()
	id, err := webGroupRoomID(c.Param("id"))
	if hs == nil || hs.Config == nil || !hs.Config.Enabled || err != nil {
		exploreFailure(c, 404, "EXPLORE_GROUP_NOT_FOUND", "Group not found")
		return
	}
	conn := db.GetDB().WithContext(c.Request.Context())
	var score MatrixGroupHotScore
	var room MatrixRoom
	if conn.Where("room_id = ?", id).First(&score).Error != nil || eligibleExploreGroups(conn).Where("room_id = ?", id).First(&room).Error != nil || room.AvatarURL == nil || !exploreGroupListable(&room, score.IsLocal) {
		exploreFailure(c, 404, "EXPLORE_GROUP_NOT_FOUND", "Group not found")
		return
	}
	c.Header("Cache-Control", "public, max-age=3600")
	serveWebGroupMXC(c, hs, *room.AvatarURL, "group-avatar", true)
}
