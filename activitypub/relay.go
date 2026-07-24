package activitypub

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// RelayContext ActivityPub 上下文.
var RelayContext = []string{"https://www.w3.org/ns/activitystreams"} //nolint:goconst

// RelayService 中继服务.
type RelayService struct {
	db *gorm.DB
}

// NewRelayService 创建中继服务.
func NewRelayService() *RelayService {
	return &RelayService{
		db: db.GetDB(),
	}
}

// getDomain 获取当前域名.
func (s *RelayService) getDomain() string {
	return systemsettings.GetDomain()
}

// GetInstanceActorURI 获取实例 Actor URI.
func (s *RelayService) GetInstanceActorURI() string {
	return fmt.Sprintf("https://%s/actor", s.getDomain())
}

// GetPublicCollectionURI 获取公共集合 URI.
func (s *RelayService) GetPublicCollectionURI() string {
	return fmt.Sprintf("https://%s/public", s.getDomain())
}

// GenerateActivityID 生成活动 ID.
func (s *RelayService) GenerateActivityID(activityType string) string {
	return fmt.Sprintf("https://%s/activities/%s/%d", s.getDomain(), activityType, time.Now().UnixNano())
}

// CreateRelay 创建中继.
func (s *RelayService) CreateRelay(inboxURL string) (*models.Relay, error) {
	// 检查是否已存在
	existing, err := models.GetRelayByInboxURL(s.db, inboxURL)
	if err == nil && existing != nil {
		return nil, errors.New("relay already exists")
	}

	relay, err := models.CreateRelay(s.db, inboxURL)
	if err != nil {
		return nil, fmt.Errorf("creating relay: %w", err)
	}

	return relay, nil
}

// EnableRelay 启用中继（发送 Follow 活动到中继服务器）.
func (s *RelayService) EnableRelay(relayID uint64) error {
	relay, err := models.GetRelayByID(s.db, relayID)
	if err != nil {
		return fmt.Errorf("relay not found: %w", err)
	}

	if relay.IsEnabled() {
		return errors.New("relay is already enabled")
	}

	// 生成 Follow 活动
	activityID := s.GenerateActivityID("follow")
	activity := map[string]any{
		"@context": RelayContext, //nolint:goconst
		"id":       activityID,
		"type":     "Follow",                                       //nolint:goconst
		"actor":    s.GetInstanceActorURI(),                        //nolint:goconst
		"object":   "https://www.w3.org/ns/activitystreams#Public", //nolint:goconst
		"to":       []string{"https://www.w3.org/ns/activitystreams#Public"},
	}

	payload, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("marshaling activity: %w", err)
	}

	// 更新中继状态
	relay.FollowActivityID = activityID

	relay.State = models.RelayStatePending

	if err := s.db.Save(relay).Error; err != nil {
		return fmt.Errorf("updating relay state: %w", err)
	}

	// 创建投递任务
	delivery := Delivery{
		ActivityID:  activityID,
		TargetInbox: relay.InboxURL,
		Payload:     string(payload),
		Status:      DeliveryStatusPending,
		Retries:     0,
	}
	if err := s.db.Create(&delivery).Error; err != nil {
		return fmt.Errorf("creating delivery: %w", err)
	}

	return nil
}

// DisableRelay 禁用中继（发送 Undo Follow 活动）.
func (s *RelayService) DisableRelay(relayID uint64) error {
	relay, err := models.GetRelayByID(s.db, relayID)
	if err != nil {
		return fmt.Errorf("relay not found: %w", err)
	}

	// 生成 Undo Follow 活动
	activityID := s.GenerateActivityID("undo")
	activity := map[string]any{
		"@context": RelayContext,
		"id":       activityID,
		"type":     "Undo", //nolint:goconst
		"actor":    s.GetInstanceActorURI(),
		"object": map[string]any{
			"id":     relay.FollowActivityID,
			"type":   "Follow",
			"actor":  s.GetInstanceActorURI(),
			"object": "https://www.w3.org/ns/activitystreams#Public",
		},
		"to": []string{"https://www.w3.org/ns/activitystreams#Public"},
	}

	payload, err := json.Marshal(activity)
	if err != nil {
		return fmt.Errorf("marshaling activity: %w", err)
	}

	// 更新中继状态
	relay.State = models.RelayStateIdle

	relay.FollowActivityID = ""

	if err := s.db.Save(relay).Error; err != nil {
		return fmt.Errorf("updating relay state: %w", err)
	}

	// 创建投递任务
	delivery := Delivery{
		ActivityID:  activityID,
		TargetInbox: relay.InboxURL,
		Payload:     string(payload),
		Status:      DeliveryStatusPending,
		Retries:     0,
	}
	if err := s.db.Create(&delivery).Error; err != nil {
		return fmt.Errorf("creating delivery: %w", err)
	}

	return nil
}

// DeleteRelay 删除中继.
func (s *RelayService) DeleteRelay(relayID uint64) error {
	relay, err := models.GetRelayByID(s.db, relayID)
	if err != nil {
		return fmt.Errorf("relay not found: %w", err)
	}

	// 如果中继已启用，先禁用
	if relay.IsEnabled() || relay.IsPending() {
		_ = s.DisableRelay(relayID)
	}

	return models.DeleteRelay(s.db, relayID)
}

// AcceptRelay 接受中继（处理来自中继服务器的 Accept 活动）.
func (s *RelayService) AcceptRelay(followActivityID string) error {
	relay, err := models.GetRelayByFollowActivityID(s.db, followActivityID)
	if err != nil {
		return nil //nolint:nilerr // not a relay Follow activity, ignore
	}

	relay.State = models.RelayStateAccepted

	return s.db.Save(relay).Error
}

// RejectRelay 拒绝中继（处理来自中继服务器的 Reject 活动）.
func (s *RelayService) RejectRelay(followActivityID string) error {
	relay, err := models.GetRelayByFollowActivityID(s.db, followActivityID)
	if err != nil {
		return nil //nolint:nilerr // 不是中继的 Follow 活动，忽略
	}

	relay.State = models.RelayStateRejected

	return s.db.Save(relay).Error
}

// GetRelayInboxes 获取所有已启用中继的 inbox URL.
func (s *RelayService) GetRelayInboxes() ([]string, error) {
	return models.GetEnabledRelayInboxes(s.db)
}

// IsRelayInbox 检查 URL 是否是中继的 inbox.
func (s *RelayService) IsRelayInbox(inboxURL string) bool {
	relay, err := models.GetRelayByInboxURL(s.db, inboxURL)
	if err != nil {
		return false
	}

	return relay.IsEnabled()
}

// GetAllRelays 获取所有中继.
func (s *RelayService) GetAllRelays() ([]models.Relay, error) {
	return models.GetAllRelays(s.db)
}

// GetRelayByID 根据 ID 获取中继.
func (s *RelayService) GetRelayByID(id uint64) (*models.Relay, error) {
	return models.GetRelayByID(s.db, id)
}

// RelaySerializer 中继序列化器（用于 API 响应）.
type RelaySerializer struct {
	ID               uint64 `json:"id"`
	InboxURL         string `json:"inbox_url"`
	FollowActivityID string `json:"follow_activity_id,omitempty"`
	State            string `json:"state"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
}

// SerializeRelay 序列化中继.
func SerializeRelay(relay *models.Relay) RelaySerializer {
	stateMap := map[models.RelayState]string{
		models.RelayStateIdle:     "idle",
		models.RelayStatePending:  "pending",
		models.RelayStateAccepted: "accepted", //nolint:goconst
		models.RelayStateRejected: "rejected",
	}

	return RelaySerializer{
		ID:               relay.ID,
		InboxURL:         relay.InboxURL,
		FollowActivityID: relay.FollowActivityID,
		State:            stateMap[relay.State],
		CreatedAt:        relay.CreatedAt.Format(time.RFC3339),
		UpdatedAt:        relay.UpdatedAt.Format(time.RFC3339),
	}
}

// SerializeRelays 序列化中继列表.
func SerializeRelays(relays []models.Relay) []RelaySerializer {
	result := make([]RelaySerializer, len(relays))
	for i, relay := range relays {
		result[i] = SerializeRelay(&relay)
	}

	return result
}
