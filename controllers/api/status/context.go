package status

import (
	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
)

// ContextResponse represents the context response for a status.
type ContextResponse struct {
	Ancestors   []*serializer.Status `json:"ancestors"`
	Descendants []*serializer.Status `json:"descendants"`
}

// Context handles GET /api/v1/statuses/:id/context
// Returns ancestors and descendants of a status.
func Context(c *gin.Context) {
	status := controllers.GetIdFromContext(&models.Status{}, c, db.GetDB(), models.StatusLoadAllQuery)
	account := controllers.GetAccount(c)

	// Thread membership is not authorization for any individual message.
	allowed, err := status.CanViewEx(db.GetDB(), account)
	if err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	if !allowed {
		c.JSON(403, gin.H{"error": "has no permission"})
		return
	}

	// Get ancestors (parent statuses in the thread)
	ancestors := make([]*models.Status, 0)
	if status.InReplyToId != nil && *status.InReplyToId > 0 {
		ancestors = getAncestors(db.GetDB(), *status.InReplyToId, 40)
	}

	// Get descendants (replies to this status)
	descendants := getDescendants(db.GetDB(), status.Id, 60, 0)

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
		Ancestors:   ancestorStatuses,
		Descendants: descendantStatuses,
	})
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
