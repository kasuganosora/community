package feed

import (
	"sort"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// StatusTimelineOrder is newest publication time first. id is only a tie-break
// so two posts from the same instant stay stable and cursors stay unique.
// Serial ids are not publication time: a federated post ingested late gets a
// higher id than posts that were published after it.
const StatusTimelineOrder = "statuses.created_at DESC, statuses.id DESC"

// TimelineRankCreatedAt marks a Redis timeline whose scores are publication
// time. Caches written with serial ids do not carry it and are rebuilt.
const TimelineRankCreatedAt = "ca1"

// TimelineScore is the Redis rank for a status. Publication time wins.
// Posts with no timestamp (in-memory fixtures) keep the historical id score
// so id-ordered fixtures stay stable.
func TimelineScore(status *models.Status) float64 {
	if status == nil {
		return 0
	}
	if status.CreatedAt != nil && !status.CreatedAt.IsZero() {
		return float64(status.CreatedAt.UTC().UnixMicro())
	}
	return float64(status.Id)
}

// ScoreForStatusID is TimelineScore for a row known only by id, used when a
// replacement reblog is promoted from the duplicate set. A missing row keeps
// the id score.
func ScoreForStatusID(id uint64) float64 {
	if id == 0 {
		return 0
	}
	conn := db.GetDB()
	if conn == nil {
		return float64(id)
	}
	var row struct {
		CreatedAt *time.Time `gorm:"column:created_at"`
	}
	err := conn.Model(&models.Status{}).Select("created_at").Where("id = ?", id).Take(&row).Error
	if err != nil {
		return float64(id)
	}
	return TimelineScore(&models.Status{Id: id, CreatedAt: row.CreatedAt})
}

type statusCursor struct {
	id        uint64
	createdAt *time.Time
	found     bool
}

func loadStatusCursors(conn *gorm.DB, ids ...uint64) (map[uint64]statusCursor, error) {
	out := make(map[uint64]statusCursor, len(ids))
	unique := make([]uint64, 0, len(ids))
	seen := map[uint64]bool{}
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return out, nil
	}
	var rows []models.Status
	// Unscoped so a soft-deleted cursor still has a publication time.
	err := conn.Unscoped().Model(&models.Status{}).Select("id", "created_at").Where("id IN ?", unique).Find(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.Id] = statusCursor{id: row.Id, createdAt: row.CreatedAt, found: true}
	}
	return out, nil
}

// applyTimelineCursors bounds a newest-first query by the cursor status's
// publication time. max_id is exclusive and older; min_id and since_id are
// exclusive lower bounds (newer). A cursor row that no longer exists falls
// back to the numeric id so a hard-deleted page boundary still terminates.
func applyTimelineCursors(query *gorm.DB, params *ListParam, cursors map[uint64]statusCursor) *gorm.DB {
	if params == nil {
		return query
	}
	if params.MaxId > 0 {
		query = olderThanCursor(query, cursors[params.MaxId], params.MaxId)
	}
	if params.MinId > 0 {
		query = newerThanCursor(query, cursors[params.MinId], params.MinId)
	}
	if params.SinceId > 0 {
		query = newerThanCursor(query, cursors[params.SinceId], params.SinceId)
	}
	return query
}

func olderThanCursor(query *gorm.DB, cursor statusCursor, id uint64) *gorm.DB {
	if !cursor.found || cursor.createdAt == nil {
		return query.Where("statuses.id < ?", id)
	}
	at := *cursor.createdAt
	return query.Where("(statuses.created_at < ? OR (statuses.created_at = ? AND statuses.id < ?))", at, at, id)
}

func newerThanCursor(query *gorm.DB, cursor statusCursor, id uint64) *gorm.DB {
	if !cursor.found || cursor.createdAt == nil {
		return query.Where("statuses.id > ?", id)
	}
	at := *cursor.createdAt
	return query.Where("(statuses.created_at > ? OR (statuses.created_at = ? AND statuses.id > ?))", at, at, id)
}

func orderStatusesByIDs(statuses models.Statuses, ids []uint64) models.Statuses {
	if len(statuses) < 2 {
		return statuses
	}
	index := make(map[uint64]int, len(ids))
	for i, id := range ids {
		index[id] = i
	}
	sort.SliceStable(statuses, func(i, j int) bool {
		left, lok := index[statuses[i].Id]
		right, rok := index[statuses[j].Id]
		if !lok {
			left = len(ids) + 1
		}
		if !rok {
			right = len(ids) + 1
		}
		return left < right
	})
	return statuses
}

func cursorIDs(params *ListParam) []uint64 {
	if params == nil {
		return nil
	}
	return []uint64{params.MaxId, params.MinId, params.SinceId}
}

// rankScore is the sorted-set score passed to a projection. Callers that know
// the publication time set Projection.Score; everyone else (notifications,
// id-only fixtures) stays on the id.
func (p Projection) rankScore() float64 {
	if p.Score != 0 {
		return p.Score
	}
	return float64(p.StatusID)
}
