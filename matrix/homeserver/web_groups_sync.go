package homeserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"gorm.io/gorm"
)

type webGroupSyncRoom struct {
	Position    uint64          `json:"p"`
	Fingerprint string          `json:"f"`
	Membership  MembershipState `json:"m"`
}
type webGroupSyncCursor struct {
	User   string                      `json:"u"`
	Issued int64                       `json:"t"`
	Rooms  map[string]webGroupSyncRoom `json:"r"`
}
type webGroupSyncResponse struct {
	NextCursor string                       `json:"next_cursor"`
	Groups     []gin.H                      `json:"groups"`
	Changed    []string                     `json:"changed_group_ids"`
	Left       []string                     `json:"left_group_ids"`
	Reset      bool                         `json:"reset"`
	Messages   map[string][]webGroupMessage `json:"messages_by_group"`
	Typing     map[string][]webGroupSender  `json:"typing_by_group"`
	HasMore    bool                         `json:"has_more"`
	// Degraded marks a response that carries no new state because the local
	// snapshot exceeded its budget (e.g. the database is busy persisting a
	// large federated join). The cursor is unchanged; the client re-polls
	// after RetryAfterMs and picks up the changes then.
	Degraded     bool `json:"degraded,omitempty"`
	RetryAfterMs int  `json:"retry_after_ms,omitempty"`
}

// webGroupSyncSnapshotBudget bounds one snapshot so a busy database (or any
// lock held by background federation work) cannot hold the long poll until a
// proxy answers 504. Variable for tests.
var webGroupSyncSnapshotBudget = 3 * time.Second

var errWebGroupSyncLimit = errors.New("web group sync limit exceeded")

func webGroupSyncLimitFailure(c *gin.Context) {
	webGroupFailure(c, http.StatusUnprocessableEntity, "sync_limit_exceeded", "群组数量或同步状态超出网页实时同步上限，请使用 Matrix 客户端；仍可查看群列表和历史，并手动刷新")
}

func handleWebGroupSync(c *gin.Context) {
	if !ensureWebGroupIdentity(c) {
		return
	}
	hs, user := GetHomeServer(), getUserID(c)
	since := c.Query("since")
	timeout, _ := strconv.Atoi(c.Query("timeout"))
	if c.Request.Method == "POST" {
		var request struct {
			Since   string `json:"since"`
			Timeout int    `json:"timeout"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 300*1024)
		if c.ShouldBindJSON(&request) != nil {
			webGroupFailure(c, 400, "invalid_cursor", "同步请求格式不正确")
			return
		}
		since = request.Since
		timeout = request.Timeout
	}
	initial := since == ""
	reset := initial
	cursor := webGroupSyncCursor{User: user, Rooms: map[string]webGroupSyncRoom{}}
	if !initial {
		if len(since) > 256*1024 {
			webGroupSyncLimitFailure(c)
			return
		}
		if webGroupDecode(since, &cursor) != nil || cursor.User != user {
			webGroupFailure(c, 400, "invalid_cursor", "同步状态已失效，请重新打开群聊")
			return
		}
		if len(cursor.Rooms) > 2000 {
			webGroupSyncLimitFailure(c)
			return
		}
		if cursor.Issued < time.Now().Add(-7*24*time.Hour).Unix() {
			reset = true
			cursor.Rooms = map[string]webGroupSyncRoom{}
		}
	}
	timeout = max(0, min(timeout, 25000))
	deadline := time.NewTimer(time.Duration(timeout) * time.Millisecond)
	defer deadline.Stop()
	// Notifications wake this process immediately. A bounded cross-process DB
	// revision check is the reliability fallback; no room has its own poller.
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		var wake <-chan struct{}
		cancel := func() {}
		if hs.SyncNotifier != nil {
			wake, cancel = hs.SyncNotifier.Subscribe(user, "web_groups")
		}
		snapCtx, snapCancel := context.WithTimeout(c.Request.Context(), webGroupSyncSnapshotBudget)
		response, err := webGroupSyncSnapshot(hs, db.GetDB().WithContext(snapCtx), user, cursor, reset)
		overBudget := err != nil && snapCtx.Err() != nil && c.Request.Context().Err() == nil
		snapCancel()
		if overBudget {
			cancel()
			if initial || reset {
				c.Header("Retry-After", "2")
				webGroupFailure(c, http.StatusServiceUnavailable, "sync_busy", "群聊同步繁忙，正在重试")
				return
			}
			c.JSON(200, webGroupSyncResponse{NextCursor: since, Groups: []gin.H{}, Changed: []string{}, Left: []string{}, Messages: map[string][]webGroupMessage{}, Typing: map[string][]webGroupSender{}, Degraded: true, RetryAfterMs: 1000})
			return
		}
		if err != nil {
			cancel()
			if errors.Is(err, errWebGroupSyncLimit) {
				webGroupSyncLimitFailure(c)
			} else if !errors.Is(err, context.Canceled) {
				webGroupInternalError(c, "synchronize rooms", err)
			}
			return
		}
		if reset || len(response.Changed) > 0 || len(response.Left) > 0 || timeout == 0 {
			cancel()
			c.JSON(200, response)
			return
		}
		select {
		case <-c.Request.Context().Done():
			cancel()
			return
		case <-deadline.C:
			cancel()
			c.JSON(200, response)
			return
		case <-ticker.C:
			cancel()
		case <-wake:
			cancel()
		}
	}
}

// Each room's stream is independently ordered by committed inserts, allowing a
// long import in one room without blocking unrelated rooms. Current membership
// is rechecked on every request, including when all notifications were lost.
func webGroupSyncSnapshot(hs *HomeServer, conn *gorm.DB, user string, cursor webGroupSyncCursor, reset bool) (webGroupSyncResponse, error) {
	response := webGroupSyncResponse{Groups: []gin.H{}, Changed: []string{}, Left: []string{}, Reset: reset, Messages: map[string][]webGroupMessage{}, Typing: map[string][]webGroupSender{}}
	var rooms []MatrixRoom
	err := webGroupRoomQuery(conn).Where("room_id IN (?)", conn.Model(&MatrixRoomMember{}).Select("room_id").Where("user_id = ? AND forgotten = ? AND membership IN ?", user, false, []MembershipState{MembershipJoin, MembershipInvite, MembershipKnock})).Order("room_id").Limit(2001).Find(&rooms).Error
	if err != nil {
		return response, err
	}
	if len(rooms) > 2000 {
		return response, errWebGroupSyncLimit
	}
	roomIDs := []string{}
	for _, room := range rooms {
		roomIDs = append(roomIDs, room.RoomID)
	}
	var members []MatrixRoomMember
	var receipts []MatrixReceipt
	var accountData []MatrixAccountData
	var typing []MatrixTyping
	var preferences []PushRule
	if len(roomIDs) > 0 {
		if err = conn.Where("room_id IN ? AND user_id = ?", roomIDs, user).Find(&members).Error; err != nil {
			return response, err
		}
		if err = conn.Where("room_id IN ? AND user_id = ?", roomIDs, user).Order("id").Find(&receipts).Error; err != nil {
			return response, err
		}
		if err = conn.Where("room_id IN ? AND user_id = ?", roomIDs, user).Order("id").Find(&accountData).Error; err != nil {
			return response, err
		}
		if err = conn.Where("user_id = ? AND scope = ? AND rule_id IN ?", user, "global", roomIDs).Order("id").Find(&preferences).Error; err != nil {
			return response, err
		}
		if err = conn.Where("room_id IN ? AND expires_at > ?", roomIDs, time.Now()).Order("room_id,user_id").Find(&typing).Error; err != nil {
			return response, err
		}
	}
	membersByRoom := map[string]MatrixRoomMember{}
	for _, m := range members {
		membersByRoom[m.RoomID] = m
	}
	receiptsByRoom := map[string][]MatrixReceipt{}
	for _, r := range receipts {
		receiptsByRoom[r.RoomID] = append(receiptsByRoom[r.RoomID], r)
	}
	dataByRoom := map[string][]MatrixAccountData{}
	for _, d := range accountData {
		if d.RoomID != nil {
			dataByRoom[*d.RoomID] = append(dataByRoom[*d.RoomID], d)
		}
	}
	preferencesByRoom := map[string][]any{}
	for _, rule := range preferences {
		preferencesByRoom[rule.RuleID] = append(preferencesByRoom[rule.RuleID], []any{rule.ID, rule.Kind, rule.Enabled, rule.Actions, rule.Conditions, rule.Pattern, rule.PriorityClass})
	}
	typingByRoom := map[string][]string{}
	for _, t := range typing {
		if t.UserID != user {
			typingByRoom[t.RoomID] = append(typingByRoom[t.RoomID], t.UserID)
		}
	}
	next := webGroupSyncCursor{User: user, Issued: time.Now().Unix(), Rooms: map[string]webGroupSyncRoom{}}
	changedRooms := []MatrixRoom{}
	budget := 500
	for _, room := range rooms {
		old, known := cursor.Rooms[room.RoomID]
		member := membersByRoom[room.RoomID]
		fingerprint := webGroupHash([]any{member.Membership, member.EventID, member.Forgotten, room.UpdatedAt, room.CurrentStateID, receiptsByRoom[room.RoomID], dataByRoom[room.RoomID], preferencesByRoom[room.RoomID], typingByRoom[room.RoomID]})
		entry := webGroupSyncRoom{Position: room.WebStreamPosition, Fingerprint: fingerprint, Membership: member.Membership}
		changed := reset || !known || old.Fingerprint != fingerprint || old.Position != room.WebStreamPosition
		if changed && member.Membership == MembershipJoin && known && !reset && old.Position < room.WebStreamPosition {
			if budget <= 0 {
				next.Rooms[room.RoomID] = old
				response.HasMore = true
				continue
			}
			var events []MatrixEvent
			limit := min(100, budget)
			if err := conn.Where("room_id = ? AND stream_position > ? AND stream_position <= ?", room.RoomID, old.Position, room.WebStreamPosition).Order("stream_position ASC").Limit(limit + 1).Find(&events).Error; err != nil {
				return response, err
			}
			if len(events) > limit {
				events = events[:limit]
				entry.Position = events[len(events)-1].StreamPosition
				response.HasMore = true
			}
			budget -= len(events)
			targets := []string{}
			for _, event := range events {
				if event.StateKey != nil {
					continue
				}
				if event.EventType == EventTypeRoomRedaction && event.Redacts != nil {
					targets = append(targets, *event.Redacts)
				} else if event.RelationTarget != "" {
					targets = append(targets, event.RelationTarget)
				} else {
					targets = append(targets, event.EventID)
				}
			}
			if len(targets) > 0 {
				var targetsRows []MatrixEvent
				if err := conn.Where("room_id = ? AND event_id IN ?", room.RoomID, targets).Find(&targetsRows).Error; err != nil {
					return response, err
				}
				// Redacting an edit or reaction changes its original message, even after
				// redaction removed the relation from the mutable content column.
				extra := []string{}
				for _, event := range targetsRows {
					if event.RelationTarget != "" {
						extra = append(extra, event.RelationTarget)
					}
				}
				targets = append(targets, extra...)
				slices.Sort(targets)
				targets = slices.Compact(targets)
				if err := conn.Where("room_id = ? AND event_id IN ? AND state_key IS NULL AND event_type <> ?", room.RoomID, targets, "m.reaction").Order("stream_position ASC,created_at ASC,event_id ASC").Find(&targetsRows).Error; err != nil {
					return response, err
				}
				reader, err := newClientHistoryReader(conn, room.RoomID, user)
				if err != nil {
					return response, err
				}
				visible, err := reader.filter(targetsRows)
				if err != nil {
					return response, err
				}
				originals := []MatrixEvent{}
				for _, event := range visible {
					if event.RelationType != "m.replace" && webGroupRelation(event.Content)["rel_type"] != "m.replace" {
						originals = append(originals, event)
					}
				}
				messages, err := webGroupMessagesDTO(hs, conn, room.RoomID, user, originals)
				if err != nil {
					return response, err
				}
				response.Messages[webGroupID(room.RoomID)] = messages
			}
		}
		next.Rooms[room.RoomID] = entry
		if changed {
			changedRooms = append(changedRooms, room)
			id := webGroupID(room.RoomID)
			response.Changed = append(response.Changed, id)
			response.Typing[id] = []webGroupSender{}
			for _, mxid := range typingByRoom[room.RoomID] {
				response.Typing[id] = append(response.Typing[id], webGroupSender{ID: mxid, DisplayName: mxid})
			}
		}
	}
	for id := range cursor.Rooms {
		if _, exists := next.Rooms[id]; !exists {
			response.Left = append(response.Left, webGroupID(id))
		}
	}
	sort.Strings(response.Left)
	if len(changedRooms) > 0 {
		response.Groups, err = webGroupDTOs(hs, conn, changedRooms, user)
		if err != nil {
			return response, err
		}
	}
	response.NextCursor = webGroupEncode(next)
	// Enforce the same parser bound before exposing a token we cannot consume.
	if len(response.NextCursor) > 256*1024 {
		return response, errWebGroupSyncLimit
	}
	return response, nil
}
