package homeserver

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
)

// ===== 房间别名 =====

// MatrixRoomAlias 房间别名.
type MatrixRoomAlias struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement"      json:"id"`
	RoomAlias string    `gorm:"uniqueIndex;size:255;not null" json:"room_alias"`
	RoomID    string    `gorm:"index;size:255;not null"       json:"room_id"`
	Creator   string    `gorm:"size:255"                      json:"creator"`
	CreatedAt time.Time `json:"created_at"`
}

func (MatrixRoomAlias) TableName() string {
	return "matrix_room_aliases"
}

// AliasService 别名服务.
type AliasService struct {
	hs *HomeServer
}

// NewAliasService 创建别名服务.
func NewAliasService(hs *HomeServer) *AliasService {
	return &AliasService{hs: hs}
}

// GetRoomIDByAlias 通过别名获取房间 ID.
func (s *AliasService) GetRoomIDByAlias(alias string) (string, error) {
	// 标准化别名格式
	if !strings.HasPrefix(alias, "#") {
		alias = "#" + alias
	}

	if !strings.Contains(alias, ":") {
		alias = alias + ":" + s.hs.Config.ServerName
	}

	var roomAlias MatrixRoomAlias

	err := db.GetDB().Where("room_alias = ?", alias).First(&roomAlias).Error
	if err != nil {
		return "", err
	}

	return roomAlias.RoomID, nil
}

// SetAlias 设置房间别名.
func (s *AliasService) SetAlias(alias, roomID, creator string) error {
	// 标准化别名格式
	if !strings.HasPrefix(alias, "#") {
		alias = "#" + alias
	}

	if !strings.Contains(alias, ":") {
		alias = alias + ":" + s.hs.Config.ServerName
	}

	// 检查别名是否已存在
	var existing MatrixRoomAlias

	err := db.GetDB().Where("room_alias = ?", alias).First(&existing).Error
	if err == nil {
		// 别名已存在，更新
		return db.GetDB().Model(&existing).Updates(map[string]any{
			"room_id": roomID, //nolint:goconst
			"creator": creator,
		}).Error
	}

	// 创建新别名
	roomAlias := &MatrixRoomAlias{
		RoomAlias: alias,
		RoomID:    roomID,
		Creator:   creator,
		CreatedAt: time.Now(),
	}

	return db.GetDB().Create(roomAlias).Error
}

// DeleteAlias 删除房间别名.
func (s *AliasService) DeleteAlias(alias string) error {
	if !strings.HasPrefix(alias, "#") {
		alias = "#" + alias
	}

	if !strings.Contains(alias, ":") {
		alias = alias + ":" + s.hs.Config.ServerName
	}

	return db.GetDB().Where("room_alias = ?", alias).Delete(&MatrixRoomAlias{}).Error
}

// GetAliasesForRoom 获取房间的所有别名.
func (s *AliasService) GetAliasesForRoom(roomID string) ([]string, error) {
	var aliases []MatrixRoomAlias

	err := db.GetDB().Where("room_id = ?", roomID).Find(&aliases).Error
	if err != nil {
		return nil, err
	}

	result := make([]string, len(aliases))
	for i, a := range aliases {
		result[i] = a.RoomAlias
	}

	return result, nil
}

// ===== 举报服务 =====

// ReportService 举报服务.
type ReportService struct {
	hs *HomeServer
}

// NewReportService 创建举报服务.
func NewReportService(hs *HomeServer) *ReportService {
	return &ReportService{hs: hs}
}

// CreateReport 创建举报.
func (s *ReportService) CreateReport(roomID, eventID, reporterID string, score int64, reason string) error {
	// 复用社区的举报模型
	now := time.Now()
	report := &models.Report{
		Category:  models.ReportCategoryOther,
		Comment:   fmt.Sprintf("Matrix Room: %s, Event: %s\nReason: %s", roomID, eventID, reason),
		CreatedAt: &now,
	}

	// 尝试关联到社区账户
	localpart, _, ok := ParseMatrixUserID(reporterID)
	if ok {
		var account models.Account
		err := db.GetDB().Where("username = ?", localpart).First(&account).Error
		if err == nil {
			report.AccountId = account.Id
		}
	}

	return db.GetDB().Create(report).Error
}

// ===== HTTP Handlers 更新 =====

// handleGetRoomAlias 获取房间别名.
func handleGetRoomAlias(c *gin.Context) {
	roomAlias := c.Param("roomAlias")
	hs := GetHomeServer()

	aliasService := NewAliasService(hs)

	roomID, err := aliasService.GetRoomIDByAlias(roomAlias)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",          //nolint:goconst
			"error":   "Room alias not found", //nolint:goconst
		})

		return
	}

	// 返回房间 ID 和服务器列表
	c.JSON(http.StatusOK, gin.H{
		"room_id": roomID,
		"servers": []string{hs.Config.ServerName},
	})
}

// handleSetRoomAlias 设置房间别名.
func handleSetRoomAlias(c *gin.Context) {
	roomAlias := c.Param("roomAlias")
	userID := getUserID(c)

	var req struct {
		RoomID string `json:"room_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",   //nolint:goconst
			"error":   "Invalid JSON", //nolint:goconst
		})

		return
	}

	hs := GetHomeServer()

	// 检查用户是否有权限设置别名（需要是房间成员）
	var member MatrixRoomMember

	err := db.GetDB().Where("room_id = ? AND user_id = ? AND membership = ?",
		req.RoomID, userID, "join").First(&member).Error
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN", //nolint:goconst
			"error":   "Not a member of this room",
		})

		return
	}

	aliasService := NewAliasService(hs)
	if err := aliasService.SetAlias(roomAlias, req.RoomID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL", //nolint:goconst
			"error":   "Failed to set alias",
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleDeleteRoomAlias 删除房间别名.
func handleDeleteRoomAlias(c *gin.Context) {
	roomAlias := c.Param("roomAlias")
	userID := getUserID(c)

	hs := GetHomeServer()
	aliasService := NewAliasService(hs)

	// 标准化别名格式
	normalizedAlias := roomAlias
	if !strings.HasPrefix(normalizedAlias, "#") {
		normalizedAlias = "#" + normalizedAlias
	}

	if !strings.Contains(normalizedAlias, ":") {
		normalizedAlias = normalizedAlias + ":" + hs.Config.ServerName
	}

	// 检查别名是否存在及其创建者
	var alias MatrixRoomAlias

	err := db.GetDB().Where("room_alias = ?", normalizedAlias).First(&alias).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room alias not found",
		})

		return
	}

	// 只有创建者或拥有足够权限的用户可以删除
	if alias.Creator != userID {
		// Check user power level via room's power_levels event
		pl, _ := hs.Room.getRoomPowerLevels(alias.RoomID)
		userPL := hs.Room.getUserPowerLevel(pl, userID)
		// Alias management requires m.room.aliases event power level (defaults to state_default=50)
		requiredPL := pl.StateDefault
		if required, ok := pl.Events["m.room.aliases"]; ok {
			requiredPL = required
		}

		if userPL < requiredPL {
			c.JSON(http.StatusForbidden, gin.H{
				"errcode": "M_FORBIDDEN",
				"error":   "Not authorized to delete this alias",
			})

			return
		}
	}

	if err := aliasService.DeleteAlias(normalizedAlias); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL",
			"error":   "Failed to delete alias",
		})

		return
	}

	// If this was the canonical alias, clear it from the state event
	var canonicalState MatrixRoomState
	if err := db.GetDB().Where("room_id = ? AND event_type = ? AND state_key = ?",
		alias.RoomID, string(EventTypeRoomCanonicalAlias), "").First(&canonicalState).Error; err == nil {
		currentAlias, _ := canonicalState.Content["alias"].(string)
		if currentAlias == normalizedAlias {
			// Send a new canonical_alias event with alias omitted (cleared)
			// Matrix spec: omit alias field rather than setting empty string
			newContent := map[string]any{}

			if altAliases, ok := canonicalState.Content["alt_aliases"].([]any); ok {
				// Filter out the deleted alias from alt_aliases
				var filtered []any

				for _, a := range altAliases {
					if s, ok := a.(string); ok && s != normalizedAlias {
						filtered = append(filtered, s)
					}
				}

				if len(filtered) > 0 {
					newContent["alt_aliases"] = filtered
				}
			}

			stateKey := ""
			hs.Event.SendRoomEvent(alias.RoomID, userID, string(EventTypeRoomCanonicalAlias), nil, newContent, &stateKey)
		}
	}

	c.JSON(http.StatusOK, gin.H{})
}

// handleGetAliases 获取房间所有别名.
func handleGetAliases(c *gin.Context) {
	roomID := c.Param("roomId")
	userID := getUserID(c)
	hs := GetHomeServer()

	// 只有房间成员可以列出别名
	var member MatrixRoomMember
	if err := db.GetDB().Where("room_id = ? AND user_id = ? AND membership IN ?", roomID, userID, []string{"join", "invite"}).First(&member).Error; err != nil { //nolint:goconst
		c.JSON(http.StatusForbidden, gin.H{
			"errcode": "M_FORBIDDEN",
			"error":   "Not a member of this room",
		})

		return
	}

	aliasService := NewAliasService(hs)

	aliases, err := aliasService.GetAliasesForRoom(roomID)
	if err != nil {
		aliases = []string{}
	}

	c.JSON(http.StatusOK, gin.H{
		"aliases": aliases,
	})
}

// handleReportRoom 报告房间事件.
func handleReportRoom(c *gin.Context) {
	roomID := c.Param("roomId")
	eventID := c.Param("eventId")
	userID := getUserID(c)

	var req struct {
		Score  int64  `json:"score"`
		Reason string `json:"reason"`
	}

	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
		})

		return
	}

	hs := GetHomeServer()
	reportService := NewReportService(hs)

	err = reportService.CreateReport(roomID, eventID, userID, req.Score, req.Reason)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL",
			"error":   "Failed to create report",
		})

		return
	}

	c.JSON(http.StatusOK, gin.H{})
}

// generateEventID 生成事件 ID.
func generateEventID() string {
	return fmt.Sprintf("$%s:%d", utils.GetRandomString(16), time.Now().UnixNano())
}
