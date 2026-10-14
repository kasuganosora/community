package systemsettings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
)

const (
	KeyRecommendationEnabled         = "config.recommendation.enabled"
	KeyRecommendationMode            = "config.recommendation.mode"
	KeyRecommendationApprovedVersion = "config.recommendation.meili_approved_version"
	KeyDiscoveryTrendsEnabled        = "config.discovery.trends_enabled"
	KeyRecommendationPolicyRevision  = "config.recommendation.policy_revision"
)

// DiscoveryConfiguration is read from the primary database, not the process
// settings cache: disabling promotion must take effect on every API replica.
type DiscoveryConfiguration struct {
	Enabled         bool   `json:"enabled"`
	Mode            string `json:"configured_mode"`
	ApprovedVersion string `json:"approved_version"`
	TrendsEnabled   bool   `json:"trends_enabled"`
	PolicyRevision  string `json:"-"`
}

func ReadDiscoveryConfiguration(ctx context.Context, conn *gorm.DB) (DiscoveryConfiguration, error) {
	result := DiscoveryConfiguration{Mode: "auto", TrendsEnabled: true}
	if conn == nil {
		return result, fmt.Errorf("discovery settings database unavailable")
	}
	keys := []string{KeyRecommendationEnabled, KeyRecommendationMode, KeyRecommendationApprovedVersion, KeyDiscoveryTrendsEnabled, KeyRecommendationPolicyRevision}
	var rows []models.SystemSetting
	if err := conn.WithContext(ctx).Where(map[string]any{"key": keys}).Find(&rows).Error; err != nil {
		return result, err
	}
	values := make(map[string]any, len(keys))
	for _, row := range rows {
		values[row.Key] = row.Val
	}
	for _, key := range keys {
		if v, ok := config.C.Other[key]; ok {
			values[key] = v
		}
	}
	if v, ok := values[KeyRecommendationEnabled]; ok {
		result.Enabled = utils.ToBool(v)
	}
	if v, ok := values[KeyDiscoveryTrendsEnabled]; ok {
		result.TrendsEnabled = utils.ToBool(v)
	}
	if v, ok := values[KeyRecommendationMode]; ok {
		if mode := utils.ToString(v); mode == "rules" || mode == "auto" {
			result.Mode = mode
		} else {
			result.Mode = "rules"
		}
	}
	if v, ok := values[KeyRecommendationApprovedVersion]; ok {
		result.ApprovedVersion = strings.TrimSpace(utils.ToString(v))
	}
	result.PolicyRevision = utils.ToString(values[KeyRecommendationPolicyRevision])
	return result, nil
}

// Include the durable change epoch so disable -> enable never revives an old
// signed cursor. Mode and approval are included for deployment overrides too.
func DiscoveryPolicyVersion(ctx context.Context, conn *gorm.DB) (string, error) {
	settings, err := ReadDiscoveryConfiguration(ctx, conn)
	if err != nil {
		return "", err
	}
	data, _ := json.Marshal([]any{settings.Enabled, settings.Mode, settings.ApprovedVersion, settings.PolicyRevision})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func DiscoveryEnabled(ctx context.Context, conn *gorm.DB) bool {
	settings, err := ReadDiscoveryConfiguration(ctx, conn)
	return err == nil && settings.Enabled
}
