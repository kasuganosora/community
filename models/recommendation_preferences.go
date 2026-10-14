package models

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"golang.org/x/text/language"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	RecommendationOff           = "off"
	RecommendationGeneric       = "generic"
	RecommendationPersonalized  = "personalized"
	recommendationPreferenceKey = "recommendation_preference"
	recommendationRevisionKey   = "recommendation_revision"
	recommendationLanguagesKey  = "recommendation_languages"
)

var ErrInvalidRecommendationPreference = errors.New("invalid recommendation preference")

type RecommendationPreference struct {
	Preference string   `json:"preference"`
	Revision   uint64   `json:"revision"`
	Languages  []string `json:"languages"`
}

func recommendationPreference(data map[string]any) RecommendationPreference {
	p := RecommendationPreference{Preference: RecommendationGeneric, Languages: []string{}}
	if value, ok := data[recommendationPreferenceKey].(string); ok && validRecommendationPreference(value) {
		p.Preference = value
	}
	// Store revisions as decimal strings so JSON round trips do not lose uint64 precision.
	switch n := data[recommendationRevisionKey].(type) {
	case string:
		p.Revision, _ = strconv.ParseUint(n, 10, 64)
	case float64:
		if n >= 0 && n < math.MaxUint64 && n == math.Trunc(n) {
			p.Revision = uint64(n)
		}
	case uint64:
		p.Revision = n
	}
	if languages, err := recommendationLanguages(data); err == nil {
		p.Languages = languages
	}
	return p
}

func normalizeRecommendationLanguages(values []string) ([]string, error) {
	if len(values) > 4 {
		return nil, ErrInvalidRecommendationPreference
	}
	result := make([]string, 0, len(values))
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" || len(raw) > 64 {
			return nil, ErrInvalidRecommendationPreference
		}
		tag, err := language.Parse(raw)
		if err != nil {
			return nil, ErrInvalidRecommendationPreference
		}
		canonical := tag.String()
		if !slices.Contains(result, canonical) {
			result = append(result, canonical)
		}
	}
	slices.Sort(result)
	return result, nil
}

func recommendationLanguages(data map[string]any) ([]string, error) {
	value, exists := data[recommendationLanguagesKey]
	if !exists || value == nil {
		return []string{}, nil
	}
	var values []string
	switch typed := value.(type) {
	case []string:
		values = typed
	case []any:
		for _, item := range typed {
			v, ok := item.(string)
			if !ok {
				return nil, ErrInvalidRecommendationPreference
			}
			values = append(values, v)
		}
	default:
		return nil, ErrInvalidRecommendationPreference
	}
	return normalizeRecommendationLanguages(values)
}

// RecommendationLanguageAllowed compares explicit reading preferences by
// primary language. Regions/scripts are canonicalized but do not reject a
// post labelled with only its primary language. Empty/invalid/undetermined
// content languages remain eligible, as unknown is not a demonstrated mismatch.
func RecommendationLanguageAllowed(contentLanguage string, preferred []string) bool {
	if len(preferred) == 0 || strings.TrimSpace(contentLanguage) == "" {
		return true
	}
	content, err := language.Parse(contentLanguage)
	if err != nil {
		return true
	}
	contentBase, _, _ := content.Raw()
	if contentBase.String() == "und" || contentBase.String() == "mul" || contentBase.String() == "zxx" {
		return true
	}
	for _, raw := range preferred {
		tag, err := language.Parse(raw)
		if err != nil {
			continue
		}
		base, _, _ := tag.Raw()
		if base == contentBase {
			return true
		}
	}
	return false
}

func validRecommendationPreference(value string) bool {
	return value == RecommendationOff || value == RecommendationGeneric || value == RecommendationPersonalized
}

func GetRecommendationPreference(conn *gorm.DB, userID uint64) (RecommendationPreference, error) {
	if userID == 0 || userID > math.MaxInt64 {
		return RecommendationPreference{}, ErrInvalidRecommendationPreference
	}
	var setting UserSetting
	err := conn.Where("user_id = ?", userID).Take(&setting).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return recommendationPreference(nil), nil
	}
	if err != nil {
		return RecommendationPreference{}, err
	}
	if _, err := recommendationLanguages(setting.Data); err != nil {
		return RecommendationPreference{}, err
	}
	return recommendationPreference(setting.Data), nil
}

// mutateUserSetting serializes concurrent preference writers and preserves
// unrelated JSON keys. The initial upsert also closes the missing-row race.
func mutateUserSetting(conn *gorm.DB, userID uint64, mutate func(map[string]any) error) (*UserSetting, error) {
	if userID == 0 || userID > math.MaxInt64 {
		return nil, ErrInvalidRecommendationPreference
	}
	var setting UserSetting
	err := conn.Transaction(func(tx *gorm.DB) error {
		seed := UserSetting{ID: uuid.NewString(), UserID: int64(userID), Data: map[string]any{}}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoNothing: true}).Create(&seed).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).Take(&setting).Error; err != nil {
			return err
		}
		if setting.Data == nil {
			setting.Data = map[string]any{}
		}
		if err := mutate(setting.Data); err != nil {
			return err
		}
		return tx.Model(&setting).Select("Data").Updates(&setting).Error
	})
	return &setting, err
}

// UpdateUserSettingData merges a server-validated patch under the same lock as
// recommendation preferences. Callers must not accept arbitrary client keys.
func UpdateUserSettingData(conn *gorm.DB, userID uint64, patch map[string]any) (*UserSetting, error) {
	return mutateUserSetting(conn, userID, func(data map[string]any) error {
		for key, value := range patch {
			if key == recommendationPreferenceKey || key == recommendationRevisionKey || key == recommendationLanguagesKey {
				return ErrInvalidRecommendationPreference
			}
			data[key] = value
		}
		return nil
	})
}

func bumpRecommendationRevision(data map[string]any) error {
	p := recommendationPreference(data)
	if p.Revision == math.MaxUint64 {
		return errors.New("recommendation revision exhausted")
	}
	data[recommendationRevisionKey] = strconv.FormatUint(p.Revision+1, 10)
	return nil
}

func SetRecommendationPreference(conn *gorm.DB, userID uint64, value string) (RecommendationPreference, error) {
	return SetRecommendationPreferences(conn, userID, value, nil)
}

// SetRecommendationPreferences atomically changes the reading mode and, when
// non-nil, the explicit language range. Nil preserves languages; [] clears it.
func SetRecommendationPreferences(conn *gorm.DB, userID uint64, value string, languages *[]string) (RecommendationPreference, error) {
	if !validRecommendationPreference(value) {
		return RecommendationPreference{}, ErrInvalidRecommendationPreference
	}
	var normalized []string
	if languages != nil {
		var err error
		normalized, err = normalizeRecommendationLanguages(*languages)
		if err != nil {
			return RecommendationPreference{}, err
		}
	}
	setting, err := mutateUserSetting(conn, userID, func(data map[string]any) error {
		previousLanguages, err := recommendationLanguages(data)
		if err != nil && languages == nil {
			return err
		}
		if err == nil && recommendationPreference(data).Preference == value && (languages == nil || slices.Equal(previousLanguages, normalized)) {
			return nil
		}
		if err := bumpRecommendationRevision(data); err != nil {
			return err
		}
		data[recommendationPreferenceKey] = value
		if languages != nil {
			data[recommendationLanguagesKey] = normalized
		}
		return nil
	})
	if err != nil {
		return RecommendationPreference{}, err
	}
	return recommendationPreference(setting.Data), nil
}

func SetRecommendationLanguages(conn *gorm.DB, userID uint64, languages []string) (RecommendationPreference, error) {
	normalized, err := normalizeRecommendationLanguages(languages)
	if err != nil {
		return RecommendationPreference{}, err
	}
	setting, err := mutateUserSetting(conn, userID, func(data map[string]any) error {
		previous, readErr := recommendationLanguages(data)
		if readErr == nil && slices.Equal(previous, normalized) {
			return nil
		}
		if err := bumpRecommendationRevision(data); err != nil {
			return err
		}
		data[recommendationLanguagesKey] = normalized
		return nil
	})
	if err != nil {
		return RecommendationPreference{}, err
	}
	return recommendationPreference(setting.Data), nil
}

func bumpRecommendationAccountRevision(conn *gorm.DB, accountID uint64) error {
	var account Account
	if err := conn.Select("id", "user_id").Take(&account, accountID).Error; err != nil {
		return err
	}
	if account.UserId == 0 {
		return ErrInvalidRecommendationPreference
	}
	_, err := mutateUserSetting(conn, account.UserId, bumpRecommendationRevision)
	return err
}
