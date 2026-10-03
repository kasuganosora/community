package homeserver

import (
	"strings"

	"gorm.io/gorm"
)

// unreadSnapshot is request-local. Reading receipt positions together with their
// event timestamps avoids one event lookup per thread receipt. Only notifiable,
// potentially unread event content is loaded, never federation PDU/state blobs.
type unreadSnapshot struct {
	all     *UnreadNotifications
	main    *UnreadNotifications
	threads map[string]*UnreadNotifications
}

type unreadReceiptPosition struct {
	ThreadID    string
	ReceiptType string
	ReceiptTS   int64
	EventTS     int64
}

func readUnreadSnapshot(conn *gorm.DB, roomID, userID string, threaded bool) (*unreadSnapshot, error) {
	result := &unreadSnapshot{all: &UnreadNotifications{}, main: &UnreadNotifications{}, threads: make(map[string]*UnreadNotifications)}
	var positions []unreadReceiptPosition
	err := conn.Table("matrix_receipts AS receipt").
		Select("receipt.thread_id, receipt.receipt_type, receipt.ts AS receipt_ts, event.origin_server_ts AS event_ts").
		Joins("JOIN matrix_events AS event ON event.event_id = receipt.event_id").
		Where("receipt.room_id = ? AND receipt.user_id = ? AND receipt.receipt_type IN ?", roomID, userID, []string{"m.read", "m.read.private"}).
		Order("receipt.id ASC").Scan(&positions).Error
	if err != nil {
		return nil, err
	}
	maximum := map[string]int64{}
	// Public/private positions are independent. Preserve the latest receipt
	// within each type, then apply the further position across both types.
	type scopeType struct{ scope, kind string }
	latestByType := map[scopeType]unreadReceiptPosition{}
	threadByType := map[scopeType]int64{}
	for _, p := range positions {
		maximum[p.ThreadID] = max(maximum[p.ThreadID], p.EventTS)
		key := scopeType{p.ThreadID, p.ReceiptType}
		if old, ok := latestByType[key]; !ok || p.ReceiptTS > old.ReceiptTS {
			latestByType[key] = p
		}
		threadByType[key] = p.EventTS
	}
	latest := map[string]unreadReceiptPosition{}
	threadPosition := map[string]int64{}
	for key, p := range latestByType {
		if old, ok := latest[key.scope]; !ok || p.EventTS > old.EventTS {
			latest[key.scope] = p
		}
	}
	for key, ts := range threadByType {
		threadPosition[key.scope] = max(threadPosition[key.scope], ts)
	}

	lower := maximum[""]
	if threaded {
		lower = latest[""].EventTS
	}
	query := conn.Model(&MatrixEvent{}).Select("event_type, content, origin_server_ts").
		Where("room_id = ? AND sender != ? AND event_type IN ?", roomID, userID, []EventType{EventTypeRoomMessage, "m.room.encrypted"})
	if lower > 0 {
		query = query.Where("origin_server_ts > ?", lower)
	}
	var events []MatrixEvent
	if err := query.Find(&events).Error; err != nil {
		return nil, err
	}
	increment := func(count *UnreadNotifications, evt *MatrixEvent) {
		count.NotificationCount++
		if body, ok := evt.Content["body"].(string); ok && strings.Contains(body, userID) {
			count.HighlightCount++
		}
	}
	for i := range events {
		evt := &events[i]
		if !shouldNotify(evt) {
			continue
		}
		isThread, root := isThreadEvent(evt)
		if !threaded {
			position := max(maximum[""], maximum["main"])
			if isThread {
				position = max(maximum[""], maximum[root])
			}
			if position <= 0 || evt.OriginServerTS > position {
				increment(result.all, evt)
			}
			continue
		}
		if !isThread {
			position := max(latest[""].EventTS, latest["main"].EventTS)
			if position <= 0 || evt.OriginServerTS > position {
				increment(result.main, evt)
			}
			continue
		}
		position := max(latest[""].EventTS, threadPosition[root])
		if position > 0 && evt.OriginServerTS <= position {
			continue
		}
		if result.threads[root] == nil {
			result.threads[root] = &UnreadNotifications{}
		}
		increment(result.threads[root], evt)
	}
	if len(result.threads) == 0 {
		result.threads = nil
	}
	return result, nil
}

func (s *SyncService) getUnreadNotifications(roomID, userID string) *UnreadNotifications {
	result, err := readUnreadSnapshot(s.syncDB(), roomID, userID, false)
	if err != nil {
		return &UnreadNotifications{}
	}
	return result.all
}
func (s *SyncService) getUnreadNotificationsMainTimeline(roomID, userID string) *UnreadNotifications {
	result, err := readUnreadSnapshot(s.syncDB(), roomID, userID, true)
	if err != nil {
		return &UnreadNotifications{}
	}
	return result.main
}
func (s *SyncService) getUnreadThreadNotifications(roomID, userID string) map[string]*UnreadNotifications {
	result, err := readUnreadSnapshot(s.syncDB(), roomID, userID, true)
	if err != nil {
		return nil
	}
	return result.threads
}
