package homeserver

import (
	"slices"
	"time"

	"github.com/kasuganosora/community/log"
	"gorm.io/gorm"
)

// One historical reader is reused across candidate pages of a single room.
// Pagination advances by the last examined event, never by a filtered result.
func (s *SyncService) visibleSyncEvents(roomID, userID string, events []MatrixEvent, reader **clientHistoryReader) ([]MatrixEvent, error) {
	if len(events) == 0 {
		return events, nil
	}
	if *reader == nil {
		var err error
		*reader, err = newClientHistoryReader(s.syncDB(), roomID, userID)
		if err != nil {
			return nil, err
		}
	}
	return (*reader).filter(events)
}

func (s *SyncService) readSyncTimeline(query *gorm.DB, roomID, userID string, limit int, arrivalOrder bool, reader **clientHistoryReader, filter *parsedEventFilter) ([]MatrixEvent, error) {
	if filter != nil {
		if len(filter.Types) > 0 && !slices.Contains(filter.Types, "*") {
			query = query.Where("event_type IN ?", filter.Types)
		}
		if slices.Contains(filter.NotTypes, "*") {
			query = query.Where("1 = 0")
		} else if len(filter.NotTypes) > 0 {
			query = query.Where("event_type NOT IN ?", filter.NotTypes)
		}
	}
	accepted := make([]MatrixEvent, 0)
	chunk := min(max(limit, 32), 256)
	scan := newHistoryScan() // a room of unprovable-history state events must not be walked to the end
	var cursor *MatrixEvent
	for len(accepted) < limit {
		page := query.Session(&gorm.Session{})
		if arrivalOrder {
			page = page.Order("created_at DESC, origin_server_ts DESC, depth DESC, event_id DESC")
			if cursor != nil {
				page = page.Where("created_at < ? OR (created_at = ? AND (origin_server_ts < ? OR (origin_server_ts = ? AND (depth < ? OR (depth = ? AND event_id < ?)))))", cursor.CreatedAt, cursor.CreatedAt, cursor.OriginServerTS, cursor.OriginServerTS, cursor.Depth, cursor.Depth, cursor.EventID)
			}
		} else {
			page = page.Order("origin_server_ts DESC, event_id DESC")
			if cursor != nil {
				page = page.Where("origin_server_ts < ? OR (origin_server_ts = ? AND event_id < ?)", cursor.OriginServerTS, cursor.OriginServerTS, cursor.EventID)
			}
		}
		var rows []MatrixEvent
		if err := page.Limit(chunk).Find(&rows).Error; err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			break
		}
		visible, err := s.visibleSyncEvents(roomID, userID, rows, reader)
		if err != nil {
			return nil, err
		}
		accepted = append(accepted, visible...)
		cursor = &rows[len(rows)-1]
		scan.add(len(rows))
		if len(rows) < chunk {
			break
		}
		if scan.exceeded() {
			log.Info("matrix sync room=%s user=%s: timeline scan stopped after %d candidates in %s with %d visible", roomID, userID, scan.scanned, scan.elapsed().Round(time.Millisecond), len(accepted))
			break
		}
	}
	if len(accepted) > limit {
		accepted = accepted[:limit]
	}
	return accepted, nil
}
