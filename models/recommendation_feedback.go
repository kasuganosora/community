package models

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrInvalidRecommendationFeedback = errors.New("invalid recommendation feedback")

// RecommendationFeedback stores explicit user or site exclusions, never
// inferred disinterest. Site owner 0 is independent of the administering user.
type RecommendationFeedback struct {
	ID         uint64     `gorm:"primaryKey;autoIncrement" json:"id,string"`
	Scope      string     `gorm:"size:8;not null;uniqueIndex:idx_recommendation_feedback,priority:1" json:"scope"`
	OwnerID    uint64     `gorm:"not null;uniqueIndex:idx_recommendation_feedback,priority:2" json:"owner_id,string"`
	Action     string     `gorm:"size:32;not null;uniqueIndex:idx_recommendation_feedback,priority:3" json:"action"`
	TargetKind string     `gorm:"size:16;not null;uniqueIndex:idx_recommendation_feedback,priority:4" json:"target_kind"`
	TargetKey  string     `gorm:"size:64;not null;uniqueIndex:idx_recommendation_feedback,priority:5" json:"target_key"`
	Reason     string     `gorm:"size:500;not null;default:''" json:"reason,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ExpiresAt  *time.Time `gorm:"index" json:"expires_at"`
}

func (RecommendationFeedback) TableName() string { return "recommendation_feedback" }

// EnsureRecommendationFeedbackSchema preserves TagKey's exact Unicode
// identity on MySQL, whose default collation may otherwise merge cafe/café.
// Call after AutoMigrate, including in integration fixtures.
func EnsureRecommendationFeedbackSchema(conn *gorm.DB) error {
	if !dialect.IsMySQL(conn) {
		return nil
	}
	var collation string
	if err := conn.Raw("SELECT COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'recommendation_feedback' AND COLUMN_NAME = 'target_key'").Scan(&collation).Error; err != nil {
		return err
	}
	if collation == "utf8mb4_bin" {
		return nil
	}
	if collation == "" {
		return errors.New("recommendation feedback schema has not been migrated")
	}
	return conn.Exec("ALTER TABLE recommendation_feedback MODIFY target_key VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NOT NULL").Error
}

func normalizeRecommendationFeedback(scope, action, kind, key string) (string, error) {
	valid := false
	if scope == "site" {
		valid = action == "suppress" && (kind == "account" || kind == "status" || kind == "tag")
	}
	if scope == "user" {
		valid = ((action == "dismiss_suggestion" || action == "hide_author") && kind == "account") || (action == "hide_status" && kind == "status") || (action == "hide_tag" && kind == "tag")
	}
	if !valid {
		return "", ErrInvalidRecommendationFeedback
	}
	if kind == "tag" {
		key = TagKey(key)
		if !ValidTagName(key) || len([]rune(key)) > 64 {
			return "", ErrInvalidRecommendationFeedback
		}
		return key, nil
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "+") || strings.HasPrefix(key, "-") {
		return "", ErrInvalidRecommendationFeedback
	}
	id, err := strconv.ParseUint(key, 10, 64)
	if err != nil || id == 0 || id > math.MaxInt64 {
		return "", ErrInvalidRecommendationFeedback
	}
	return strconv.FormatUint(id, 10), nil
}

func PutRecommendationFeedback(conn *gorm.DB, ownerID uint64, action, kind, key string) (*RecommendationFeedback, error) {
	if ownerID == 0 {
		return nil, ErrInvalidRecommendationFeedback
	}
	return putRecommendationFeedback(conn, "user", ownerID, action, kind, key)
}

// PutSiteRecommendationFeedback is only for authenticated administrator
// handlers; those handlers must record the actor and reason in the audit log.
func PutSiteRecommendationFeedback(conn *gorm.DB, kind, key string) (*RecommendationFeedback, error) {
	return putRecommendationFeedback(conn, "site", 0, "suppress", kind, key)
}

func putRecommendationFeedback(conn *gorm.DB, scope string, ownerID uint64, action, kind, key string) (*RecommendationFeedback, error) {
	key, err := normalizeRecommendationFeedback(scope, action, kind, key)
	if err != nil {
		return nil, err
	}
	row := RecommendationFeedback{Scope: scope, OwnerID: ownerID, Action: action, TargetKind: kind, TargetKey: key}
	err = conn.Transaction(func(tx *gorm.DB) error {
		if scope == "site" && kind != "tag" { // Admin governance validates existence; user deny rules must not become an existence oracle for private post IDs.
			var count int64
			model := any(&Account{})
			if kind == "status" {
				model = &Status{}
			}
			if err := tx.Model(model).Where("id = ?", key).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return ErrInvalidRecommendationFeedback
			}
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "scope"}, {Name: "owner_id"}, {Name: "action"}, {Name: "target_kind"}, {Name: "target_key"}}, DoUpdates: clause.Assignments(map[string]any{"updated_at": time.Now().UTC(), "expires_at": nil})}).Create(&row).Error; err != nil {
			return err
		}
		if scope == "user" {
			if err := bumpRecommendationAccountRevision(tx, ownerID); err != nil {
				return err
			}
		}
		return tx.Where("scope = ? AND owner_id = ? AND action = ? AND target_kind = ? AND target_key = ?", scope, ownerID, action, kind, key).Take(&row).Error
	})
	return &row, err
}

func DeleteRecommendationFeedback(conn *gorm.DB, ownerID uint64, action, kind, key string) error {
	if ownerID == 0 {
		return ErrInvalidRecommendationFeedback
	}
	return deleteRecommendationFeedback(conn, "user", ownerID, action, kind, key)
}

func DeleteSiteRecommendationFeedback(conn *gorm.DB, kind, key string) error {
	return deleteRecommendationFeedback(conn, "site", 0, "suppress", kind, key)
}

func deleteRecommendationFeedback(conn *gorm.DB, scope string, ownerID uint64, action, kind, key string) error {
	key, err := normalizeRecommendationFeedback(scope, action, kind, key)
	if err != nil {
		return err
	}
	return conn.Transaction(func(tx *gorm.DB) error {
		result := tx.Where("scope = ? AND owner_id = ? AND action = ? AND target_kind = ? AND target_key = ?", scope, ownerID, action, kind, key).Delete(&RecommendationFeedback{})
		if result.Error != nil {
			return result.Error
		}
		if scope == "user" && result.RowsAffected > 0 {
			return bumpRecommendationAccountRevision(tx, ownerID)
		}
		return nil
	})
}

func ListRecommendationFeedback(conn *gorm.DB, ownerID uint64) ([]RecommendationFeedback, error) {
	rows := []RecommendationFeedback{}
	if ownerID == 0 {
		return rows, ErrInvalidRecommendationFeedback
	}
	err := conn.Where("scope = ? AND owner_id = ?", "user", ownerID).Order("id DESC").Find(&rows).Error
	return rows, err
}

func ListSiteRecommendationFeedback(conn *gorm.DB) ([]RecommendationFeedback, error) {
	rows := []RecommendationFeedback{}
	err := conn.Where("scope = ? AND owner_id = ?", "site", 0).Order("id DESC").Find(&rows).Error
	return rows, err
}

func EffectiveRecommendationFeedback(conn *gorm.DB, ownerID uint64) ([]RecommendationFeedback, error) {
	rows := []RecommendationFeedback{}
	q := conn.Where("scope = ? AND owner_id = ?", "site", 0)
	if ownerID != 0 {
		q = conn.Where("(scope = ? AND owner_id = ?) OR (scope = ? AND owner_id = ?)", "site", 0, "user", ownerID)
	}
	err := q.Where("expires_at IS NULL OR expires_at > ?", time.Now().UTC()).Find(&rows).Error
	return rows, err
}

// DeleteUserRecommendationFeedback is an account erasure/reset operation.
// It intentionally cannot remove the independent site-level governance rows.
func DeleteUserRecommendationFeedback(conn *gorm.DB, ownerID uint64) error {
	if ownerID == 0 {
		return ErrInvalidRecommendationFeedback
	}
	return conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("scope = ? AND owner_id = ?", "user", ownerID).Delete(&RecommendationFeedback{}).Error; err != nil {
			return err
		}
		return bumpRecommendationAccountRevision(tx, ownerID)
	})
}
