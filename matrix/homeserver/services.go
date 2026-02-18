package homeserver

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// ===== 房间别名 =====

// MatrixRoomAlias 房间别名
type MatrixRoomAlias struct {
	ID        uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	RoomAlias string    `gorm:"uniqueIndex;size:255;not null" json:"room_alias"`
	RoomID    string    `gorm:"index;size:255;not null" json:"room_id"`
	Creator   string    `gorm:"size:255" json:"creator"`
	CreatedAt time.Time `json:"created_at"`
}

func (MatrixRoomAlias) TableName() string {
	return "matrix_room_aliases"
}

// AliasService 别名服务
type AliasService struct {
	hs *HomeServer
}

// NewAliasService 创建别名服务
func NewAliasService(hs *HomeServer) *AliasService {
	return &AliasService{hs: hs}
}

// GetRoomIDByAlias 通过别名获取房间 ID
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

// SetAlias 设置房间别名
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
		return db.GetDB().Model(&existing).Updates(map[string]interface{}{
			"room_id": roomID,
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

// DeleteAlias 删除房间别名
func (s *AliasService) DeleteAlias(alias string) error {
	if !strings.HasPrefix(alias, "#") {
		alias = "#" + alias
	}
	if !strings.Contains(alias, ":") {
		alias = alias + ":" + s.hs.Config.ServerName
	}
	return db.GetDB().Where("room_alias = ?", alias).Delete(&MatrixRoomAlias{}).Error
}

// GetAliasesForRoom 获取房间的所有别名
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

// ReportService 举报服务
type ReportService struct {
	hs *HomeServer
}

// NewReportService 创建举报服务
func NewReportService(hs *HomeServer) *ReportService {
	return &ReportService{hs: hs}
}

// CreateReport 创建举报
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
		if err := db.GetDB().Where("username = ?", localpart).First(&account).Error; err == nil {
			report.AccountId = account.Id
		}
	}
	
	return db.GetDB().Create(report).Error
}

// ===== HTTP Handlers 更新 =====

// handleGetRoomAlias 获取房间别名
func handleGetRoomAlias(c *gin.Context) {
	roomAlias := c.Param("roomAlias")
	hs := GetHomeServer()
	
	aliasService := NewAliasService(hs)
	roomID, err := aliasService.GetRoomIDByAlias(roomAlias)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room alias not found",
		})
		return
	}
	
	// 返回房间 ID 和服务器列表
	c.JSON(http.StatusOK, gin.H{
		"room_id":  roomID,
		"servers":  []string{hs.Config.ServerName},
	})
}

// handleSetRoomAlias 设置房间别名
func handleSetRoomAlias(c *gin.Context) {
	roomAlias := c.Param("roomAlias")
	userID := getUserID(c)
	
	var req struct {
		RoomID string `json:"room_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
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
			"errcode": "M_FORBIDDEN",
			"error":   "Not a member of this room",
		})
		return
	}
	
	aliasService := NewAliasService(hs)
	if err := aliasService.SetAlias(roomAlias, req.RoomID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL",
			"error":   "Failed to set alias",
		})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{})
}

// handleDeleteRoomAlias 删除房间别名
func handleDeleteRoomAlias(c *gin.Context) {
	roomAlias := c.Param("roomAlias")
	userID := getUserID(c)
	
	hs := GetHomeServer()
	aliasService := NewAliasService(hs)
	
	// 检查别名是否存在及其创建者
	var alias MatrixRoomAlias
	err := db.GetDB().Where("room_alias = ?", "#"+roomAlias+":"+hs.Config.ServerName).First(&alias).Error
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"errcode": "M_NOT_FOUND",
			"error":   "Room alias not found",
		})
		return
	}
	
	// 只有创建者或房间管理员可以删除
	if alias.Creator != userID {
		// 检查是否是房间管理员
		var member MatrixRoomMember
		err = db.GetDB().Where("room_id = ? AND user_id = ?", alias.RoomID, userID).First(&member).Error
		if err != nil || member.PowerLevel < 50 { // 50 通常是管理员级别
			c.JSON(http.StatusForbidden, gin.H{
				"errcode": "M_FORBIDDEN",
				"error":   "Not authorized to delete this alias",
			})
			return
		}
	}
	
	if err := aliasService.DeleteAlias(roomAlias); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL",
			"error":   "Failed to delete alias",
		})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{})
}

// handleGetAliases 获取房间所有别名
func handleGetAliases(c *gin.Context) {
	roomID := c.Param("roomId")
	hs := GetHomeServer()
	
	aliasService := NewAliasService(hs)
	aliases, err := aliasService.GetAliasesForRoom(roomID)
	if err != nil {
		aliases = []string{}
	}
	
	c.JSON(http.StatusOK, gin.H{
		"aliases": aliases,
	})
}

// handleReportRoom 报告房间事件
func handleReportRoom(c *gin.Context) {
	roomID := c.Param("roomId")
	eventID := c.Param("eventId")
	userID := getUserID(c)
	
	var req struct {
		Score  int64  `json:"score"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"errcode": "M_BAD_JSON",
			"error":   "Invalid JSON",
		})
		return
	}
	
	hs := GetHomeServer()
	reportService := NewReportService(hs)
	
	if err := reportService.CreateReport(roomID, eventID, userID, req.Score, req.Reason); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"errcode": "M_INTERNAL",
			"error":   "Failed to create report",
		})
		return
	}
	
	c.JSON(http.StatusOK, gin.H{})
}

// generateEventID 生成事件 ID
func generateEventID() string {
	return fmt.Sprintf("$%s:%d", generateRandomString(16), time.Now().UnixNano())
}

// generateRandomString 生成随机字符串
func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}
