package status

import (
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// ContextResponse represents the context response for a status.
type ContextResponse struct {
	Ancestors     []*serializer.Status `json:"ancestors"`
	Descendants   []*serializer.Status `json:"descendants"`
	PinnedReplyId *string              `json:"pinned_reply_id,omitempty"`
}

// Context handles GET /api/v1/statuses/:id/context
// Returns ancestors and descendants of a status.
// Supports optional query parameters:
//   - sort=hot (default: chronological) to rank replies by engagement score
//   - author_only=true to show only replies written by the parent status author
func Context(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)
	account := controllers.GetAccount(c)

	// Thread membership is not authorization for any individual message.
	abortUnlessVisible(c, db.GetDB(), status, account)
	var err error

	// A reply whose parent is not stored yet is fetched from its origin
	// (signed, for authorized-fetch servers) and linked before the walk.
	if err := activitypub.LoadMissingReplyAncestors(c.Request.Context(), status); err != nil {
		controllers.AbortIfError(c, err)
		return
	}

	// Get ancestors (parent statuses in the thread)
	ancestors := make([]*models.Status, 0)
	if status.InReplyToId != nil && *status.InReplyToId > 0 {
		ancestors = getAncestors(db.GetDB(), *status.InReplyToId, 40)
	}

	// Get descendants (replies to this status)
	descendants := getDescendants(db.GetDB(), status.Id, 60, 0)
	if status.PinnedReplyId != nil && *status.PinnedReplyId > 0 {
		pinnedID := *status.PinnedReplyId
		found := false
		for _, d := range descendants {
			if d != nil && d.Id == pinnedID {
				found = true
				break
			}
		}
		if !found {
			var pinned models.Status
			if err := models.StatusLoadAllQuery(db.GetDB()).First(&pinned, pinnedID).Error; err == nil {
				if isReplyDescendant(db.GetDB(), status.Id, &pinned, 20) {
					descendants = append(descendants, &pinned)
				}
			}
		}
	}

	// Load extended fields for all statuses
	allStatuses := append(append([]*models.Status{}, ancestors...), descendants...)
	for i := range allStatuses {
		if allStatuses[i] != nil {
			_ = allStatuses[i].CheckAndLoadAllExtendFields(db.GetDB())
		}
	}

	ancestors, err = visibleThreadStatuses(db.GetDB(), ancestors, account)
	if err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	descendants, err = visibleThreadStatuses(db.GetDB(), descendants, account)
	if err != nil {
		controllers.AbortIfError(c, err)
		return
	}

	// Optional "author_only=true" filter (keeps only replies by the root status author).
	if strings.EqualFold(c.Query("author_only"), "true") || c.Query("author_only") == "1" {
		filtered := make([]*models.Status, 0, len(descendants))
		for _, d := range descendants {
			if d.AccountId == status.AccountId {
				filtered = append(filtered, d)
			}
		}
		descendants = filtered
	}

	// Optional "sort=hot" engagement ranking (openBshi reply hot-sort pattern).
	if strings.EqualFold(c.Query("sort"), "hot") && len(descendants) > 1 {
		sort.SliceStable(descendants, func(i, j int) bool {
			si := replyHotScore(descendants[i])
			sj := replyHotScore(descendants[j])
			if si != sj {
				return si > sj
			}
			return descendants[i].Id < descendants[j].Id
		})
	}

	// Promote author-pinned reply to the top of descendants when present and visible.
	var pinnedReplyStr *string
	if status.PinnedReplyId != nil && *status.PinnedReplyId > 0 {
		pinnedID := *status.PinnedReplyId
		for idx, d := range descendants {
			if d.Id == pinnedID {
				if idx > 0 {
					copy(descendants[1:idx+1], descendants[0:idx])
					descendants[0] = d
				}
				s := strconv.FormatUint(pinnedID, 10)
				pinnedReplyStr = &s
				break
			}
		}
	}

	// Serialize
	ancestorStatuses, err := serializer.ToStatuses(ancestors, account)
	if err != nil {
		controllers.AbortIfError(c, err)

		return
	}

	descendantStatuses, err := serializer.ToStatuses(descendants, account)
	if err != nil {
		controllers.AbortIfError(c, err)

		return
	}

	c.JSON(200, ContextResponse{
		Ancestors:     ancestorStatuses,
		Descendants:   descendantStatuses,
		PinnedReplyId: pinnedReplyStr,
	})
}

func replyHotScore(s *models.Status) uint64 {
	if s == nil {
		return 0
	}
	return s.FavouritesCount*2 + s.ReblogsCount*3 + s.RepliesCount*2
}

// getAncestors recursively gets parent statuses.
func getAncestors(dbConn *gorm.DB, statusId uint64, limit int) []*models.Status {
	ancestors := make([]*models.Status, 0)
	visited := make(map[uint64]bool)

	currentId := statusId
	for len(ancestors) < limit && currentId > 0 {
		if visited[currentId] {
			break
		}

		visited[currentId] = true

		parent := &models.Status{}
		query := dbConn.Model(&models.Status{})
		query = models.StatusLoadAllQuery(query)

		err := query.First(parent, currentId).Error
		if err != nil {
			break
		}
		if linkErr := models.LinkStoredReplyParents(dbConn, []*models.Status{parent}, systemsettings.GetBaseURL()); linkErr != nil {
			break
		}

		ancestors = append([]*models.Status{parent}, ancestors...)

		if parent.InReplyToId == nil || *parent.InReplyToId == 0 {
			break
		}

		currentId = *parent.InReplyToId
	}

	return ancestors
}

// getDescendants recursively gets child statuses.
func getDescendants(dbConn *gorm.DB, statusId uint64, limit int, depth int) []*models.Status {
	if depth > 20 { // Depth limit to prevent infinite recursion
		return []*models.Status{}
	}

	descendants := make([]*models.Status, 0)

	// Find direct replies
	replies := make([]*models.Status, 0)
	query := dbConn.Model(&models.Status{}).
		Where("in_reply_to_id = ?", statusId).
		Order("created_at ASC").
		Limit(limit)

	query = models.StatusLoadAllQuery(query)

	err := query.Find(&replies).Error
	if err != nil {
		return descendants
	}

	for _, reply := range replies {
		if len(descendants) >= limit {
			break
		}

		descendants = append(descendants, reply)

		// Recursively get replies to this reply
		childReplies := getDescendants(dbConn, reply.Id, limit-len(descendants), depth+1)
		descendants = append(descendants, childReplies...)
	}

	return descendants
}

func visibleThreadStatuses(conn *gorm.DB, statuses []*models.Status, viewer *models.Account) ([]*models.Status, error) {
	result := make([]*models.Status, 0, len(statuses))
	for _, status := range statuses {
		allowed, err := status.CanViewEx(conn, viewer)
		if err != nil {
			return nil, err
		}
		if allowed {
			result = append(result, status)
		}
	}
	return result, nil
}
