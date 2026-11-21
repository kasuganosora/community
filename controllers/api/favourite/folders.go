package favourite

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/controllers/api/common"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	maxBookmarkFoldersPerAccount = 50
	maxFolderTitleRunes          = 64
	maxFolderDescriptionRunes    = 256
)

type bookmarkFolderResponse struct {
	Id          string `json:"id"`
	AccountId   string `json:"account_id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	IsPublic    bool   `json:"is_public"`
	ItemsCount  uint64 `json:"items_count"`
	HasStatus   *bool  `json:"has_status,omitempty"`
	CreatedAt   any    `json:"created_at"`
	UpdatedAt   any    `json:"updated_at"`
}

func toFolderResponse(f *models.BookmarkFolder, hasStatus *bool) bookmarkFolderResponse {
	return bookmarkFolderResponse{
		Id:          strconv.FormatUint(f.Id, 10),
		AccountId:   strconv.FormatUint(f.AccountId, 10),
		Title:       f.Title,
		Description: f.Description,
		IsPublic:    f.IsPublic,
		ItemsCount:  f.ItemsCount,
		HasStatus:   hasStatus,
		CreatedAt:   f.CreatedAt,
		UpdatedAt:   f.UpdatedAt,
	}
}

type folderMutationRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	IsPublic    *bool   `json:"is_public"`
}

type folderStatusRequest struct {
	StatusId string `json:"status_id"`
}

// ListBookmarkFolders handles GET /api/v1/bookmark_folders.
// Optional query ?status_id=<id> annotates each folder with has_status
// (inspired by openBshi/app/service/main/favorite UserFolders).
func ListBookmarkFolders(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	conn := db.GetDB().WithContext(c.Request.Context())

	var folders []*models.BookmarkFolder
	if err := conn.Where("account_id = ?", account.Id).Order("id ASC").Find(&folders).Error; err != nil {
		controllers.AbortIfError(c, err)
		return
	}

	var checkStatusID uint64
	if raw := strings.TrimSpace(c.Query("status_id")); raw != "" {
		id, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || id == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status_id"})
			return
		}
		checkStatusID = id
	}

	memberSet := map[uint64]bool{}
	if checkStatusID > 0 && len(folders) > 0 {
		folderIDs := make([]uint64, 0, len(folders))
		for _, f := range folders {
			folderIDs = append(folderIDs, f.Id)
		}
		var matched []uint64
		if err := conn.Model(&models.BookmarkFolderItem{}).
			Where("account_id = ? AND status_id = ? AND folder_id IN ?", account.Id, checkStatusID, folderIDs).
			Pluck("folder_id", &matched).Error; err != nil {
			controllers.AbortIfError(c, err)
			return
		}
		for _, fid := range matched {
			memberSet[fid] = true
		}
	}

	resp := make([]bookmarkFolderResponse, 0, len(folders))
	for _, f := range folders {
		var hasPtr *bool
		if checkStatusID > 0 {
			v := memberSet[f.Id]
			hasPtr = &v
		}
		resp = append(resp, toFolderResponse(f, hasPtr))
	}
	c.JSON(http.StatusOK, resp)
}

// GetBookmarkFolder handles GET /api/v1/bookmark_folders/:id.
// Public folders can be viewed by any visitor; private folders are owner-only.
func GetBookmarkFolder(c *gin.Context) {
	viewer := controllers.GetAccount(c)
	folderID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || folderID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid folder id"})
		return
	}

	conn := db.GetDB().WithContext(c.Request.Context())
	var folder models.BookmarkFolder
	if err := conn.First(&folder, folderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "bookmark folder not found"})
			return
		}
		controllers.AbortIfError(c, err)
		return
	}
	if !folder.IsPublic && (viewer == nil || viewer.Id != folder.AccountId) {
		c.JSON(http.StatusNotFound, gin.H{"error": "bookmark folder not found"})
		return
	}
	c.JSON(http.StatusOK, toFolderResponse(&folder, nil))
}

// CreateBookmarkFolder handles POST /api/v1/bookmark_folders.
func CreateBookmarkFolder(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	var req folderMutationRequest
	if err := controllers.BindBody(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	title := ""
	if req.Title != nil {
		title = strings.TrimSpace(*req.Title)
	}
	desc := ""
	if req.Description != nil {
		desc = strings.TrimSpace(*req.Description)
	}
	if title == "" || utf8.RuneCountInString(title) > maxFolderTitleRunes {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "folder title must be 1-64 characters"})
		return
	}
	if utf8.RuneCountInString(desc) > maxFolderDescriptionRunes {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "folder description exceeds 256 characters"})
		return
	}

	conn := db.GetDB().WithContext(c.Request.Context())
	var count int64
	if err := conn.Model(&models.BookmarkFolder{}).Where("account_id = ?", account.Id).Count(&count).Error; err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	if count >= maxBookmarkFoldersPerAccount {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "maximum number of bookmark folders reached"})
		return
	}

	isPublic := false
	if req.IsPublic != nil {
		isPublic = *req.IsPublic
	}
	folder := &models.BookmarkFolder{
		AccountId:   account.Id,
		Title:       title,
		Description: desc,
		IsPublic:    isPublic,
	}
	if err := conn.Create(folder).Error; err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, toFolderResponse(folder, nil))
}

// UpdateBookmarkFolder handles PUT /api/v1/bookmark_folders/:id.
func UpdateBookmarkFolder(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	folderID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || folderID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid folder id"})
		return
	}

	var req folderMutationRequest
	if err := controllers.BindBody(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	conn := db.GetDB().WithContext(c.Request.Context())
	var folder models.BookmarkFolder
	if err := conn.First(&folder, folderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "bookmark folder not found"})
			return
		}
		controllers.AbortIfError(c, err)
		return
	}
	if folder.AccountId != account.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot modify another user's bookmark folder"})
		return
	}

	updates := map[string]any{}
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" || utf8.RuneCountInString(title) > maxFolderTitleRunes {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "folder title must be 1-64 characters"})
			return
		}
		updates["title"] = title
		folder.Title = title
	}
	if req.Description != nil {
		desc := strings.TrimSpace(*req.Description)
		if utf8.RuneCountInString(desc) > maxFolderDescriptionRunes {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "folder description exceeds 256 characters"})
			return
		}
		updates["description"] = desc
		folder.Description = desc
	}
	if req.IsPublic != nil {
		updates["is_public"] = *req.IsPublic
		folder.IsPublic = *req.IsPublic
	}

	if len(updates) > 0 {
		if err := conn.Model(&folder).Updates(updates).Error; err != nil {
			controllers.AbortIfError(c, err)
			return
		}
	}
	c.JSON(http.StatusOK, toFolderResponse(&folder, nil))
}

// DeleteBookmarkFolder handles DELETE /api/v1/bookmark_folders/:id.
func DeleteBookmarkFolder(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	folderID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || folderID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid folder id"})
		return
	}

	conn := db.GetDB().WithContext(c.Request.Context())
	var folder models.BookmarkFolder
	if err := conn.First(&folder, folderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "bookmark folder not found"})
			return
		}
		controllers.AbortIfError(c, err)
		return
	}
	if folder.AccountId != account.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot delete another user's bookmark folder"})
		return
	}

	if err := conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("folder_id = ?", folder.Id).Delete(&models.BookmarkFolderItem{}).Error; err != nil {
			return err
		}
		return tx.Delete(&folder).Error
	}); err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{})
}

// AddStatusToBookmarkFolder handles POST /api/v1/bookmark_folders/:id/statuses.
func AddStatusToBookmarkFolder(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	folderID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || folderID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid folder id"})
		return
	}

	var req folderStatusRequest
	if err := controllers.BindBody(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	statusID, err := strconv.ParseUint(strings.TrimSpace(req.StatusId), 10, 64)
	if err != nil || statusID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status_id"})
		return
	}

	conn := db.GetDB().WithContext(c.Request.Context())
	var folder models.BookmarkFolder
	if err := conn.First(&folder, folderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "bookmark folder not found"})
			return
		}
		controllers.AbortIfError(c, err)
		return
	}
	if folder.AccountId != account.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot modify another user's bookmark folder"})
		return
	}

	var target models.Status
	if err := models.StatusLoadAllQuery(conn).First(&target, statusID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "status not found"})
			return
		}
		controllers.AbortIfError(c, err)
		return
	}
	allowed, err := target.CanViewEx(conn, account)
	if err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	if !allowed {
		c.JSON(http.StatusNotFound, gin.H{"error": "status not found"})
		return
	}

	err = conn.Transaction(func(tx *gorm.DB) error {
		item := &models.BookmarkFolderItem{
			FolderId:  folder.Id,
			AccountId: account.Id,
			StatusId:  statusID,
		}
		res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(item)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			if err := tx.Model(&models.BookmarkFolder{}).
				Where("id = ?", folder.Id).
				UpdateColumn("items_count", gorm.Expr("items_count + ?", 1)).Error; err != nil {
				return err
			}
			folder.ItemsCount++
		}
		return nil
	})
	if err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, toFolderResponse(&folder, nil))
}

// RemoveStatusFromBookmarkFolder handles DELETE /api/v1/bookmark_folders/:id/statuses/:status_id.
func RemoveStatusFromBookmarkFolder(c *gin.Context) {
	account := controllers.MustGetAccount(c)
	folderID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || folderID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid folder id"})
		return
	}
	statusID, err := strconv.ParseUint(c.Param("status_id"), 10, 64)
	if err != nil || statusID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status_id"})
		return
	}

	conn := db.GetDB().WithContext(c.Request.Context())
	var folder models.BookmarkFolder
	if err := conn.First(&folder, folderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "bookmark folder not found"})
			return
		}
		controllers.AbortIfError(c, err)
		return
	}
	if folder.AccountId != account.Id {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot modify another user's bookmark folder"})
		return
	}

	err = conn.Transaction(func(tx *gorm.DB) error {
		res := tx.Where("folder_id = ? AND status_id = ?", folder.Id, statusID).Delete(&models.BookmarkFolderItem{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			if err := tx.Model(&models.BookmarkFolder{}).
				Where("id = ?", folder.Id).
				UpdateColumn("items_count", gorm.Expr("CASE WHEN items_count > 0 THEN items_count - 1 ELSE 0 END")).Error; err != nil {
				return err
			}
			if folder.ItemsCount > 0 {
				folder.ItemsCount--
			}
		}
		return nil
	})
	if err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, toFolderResponse(&folder, nil))
}

// BookmarkFolderStatuses handles GET /api/v1/bookmark_folders/:id/statuses.
// Public folders can be viewed by any visitor; private folders are owner-only.
func BookmarkFolderStatuses(c *gin.Context) {
	viewer := controllers.GetAccount(c)
	folderID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || folderID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid folder id"})
		return
	}

	limit := 20
	if raw := c.Query("limit"); raw != "" {
		if v, parseErr := strconv.Atoi(raw); parseErr == nil && v > 0 {
			limit = min(v, 80)
		}
	}
	var maxID, minID, sinceID uint64
	if raw := c.Query("max_id"); raw != "" {
		maxID, _ = strconv.ParseUint(raw, 10, 64)
	}
	if raw := c.Query("min_id"); raw != "" {
		minID, _ = strconv.ParseUint(raw, 10, 64)
	}
	if raw := c.Query("since_id"); raw != "" {
		sinceID, _ = strconv.ParseUint(raw, 10, 64)
	}

	conn := db.GetDB().WithContext(models.WithDomainSeveritiesMemo(c.Request.Context()))
	var folder models.BookmarkFolder
	if err := conn.First(&folder, folderID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "bookmark folder not found"})
			return
		}
		controllers.AbortIfError(c, err)
		return
	}
	if !folder.IsPublic && (viewer == nil || viewer.Id != folder.AccountId) {
		c.JSON(http.StatusNotFound, gin.H{"error": "bookmark folder not found"})
		return
	}

	q := conn.Model(&models.BookmarkFolderItem{}).
		Joins("JOIN statuses ON statuses.id = bookmark_folder_items.status_id").
		Where("bookmark_folder_items.folder_id = ?", folder.Id).
		Where("statuses.scheduled_at IS NULL AND statuses.deleted_at IS NULL").
		Order("bookmark_folder_items.id DESC")
	if maxID > 0 {
		q = q.Where("bookmark_folder_items.id < ?", maxID)
	}
	if minID > 0 {
		q = q.Where("bookmark_folder_items.id > ?", minID)
	}
	if sinceID > 0 {
		q = q.Where("bookmark_folder_items.id > ?", sinceID)
	}

	var items []models.BookmarkFolderItem
	if err := q.Limit(limit + 1).Find(&items).Error; err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if len(items) > 0 {
		common.SetPageLinksUint(c, items[0].Id, items[len(items)-1].Id, hasMore)
	}
	if len(items) == 0 {
		c.JSON(http.StatusOK, []*serializer.Status{})
		return
	}

	statusIDs := make([]uint64, 0, len(items))
	for _, it := range items {
		statusIDs = append(statusIDs, it.StatusId)
	}

	var loaded models.Statuses
	if err := models.StatusLoadAllQuery(conn.Model(&models.Status{})).
		Where("statuses.id IN ?", statusIDs).Find(&loaded).Error; err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	byID := make(map[uint64]*models.Status, len(loaded))
	for _, s := range loaded {
		byID[s.Id] = s
	}
	ordered := make(models.Statuses, 0, len(statusIDs))
	for _, sid := range statusIDs {
		if s := byID[sid]; s != nil {
			ordered = append(ordered, s)
		}
	}

	visible, err := models.FilterVisibleStatuses(conn, viewer, ordered)
	if err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	serialized, err := serializer.ToStatusesContext(c.Request.Context(), visible, viewer)
	if err != nil {
		controllers.AbortIfError(c, err)
		return
	}
	c.JSON(http.StatusOK, serialized)
}
