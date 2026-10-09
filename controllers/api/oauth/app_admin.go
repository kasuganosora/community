package oauth

import (
	"errors"
	"time"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// ErrAppNotFound is returned when an application id does not exist.
var ErrAppNotFound = errors.New("oauth application not found")

// AppSummary is one row of the application list.
type AppSummary struct {
	Id        uint64     `json:"id,string"`
	Name      string     `json:"name"`
	Website   string     `json:"website"`
	ClientId  string     `json:"client_id"`
	Scopes    string     `json:"scopes"`
	CreatedAt *time.Time `json:"created_at"`
	// Tokens counts access/refresh tokens that are neither revoked nor expired.
	Tokens int64 `json:"active_tokens"`
}

// ListApplications returns the registered applications, newest first. It never
// includes client secrets.
func ListApplications(conn *gorm.DB) ([]AppSummary, error) {
	var apps []OAuthApplication
	if err := conn.Order("id DESC").Find(&apps).Error; err != nil {
		return nil, err
	}
	out := make([]AppSummary, 0, len(apps))
	for _, a := range apps {
		var n int64
		err := conn.Model(&models.AuthToken{}).
			Where("application_id = ? AND revoke IS NULL AND (expired_at IS NULL OR expired_at > ?)", a.Id, time.Now()).
			Count(&n).Error
		if err != nil {
			return nil, err
		}
		out = append(out, AppSummary{Id: a.Id, Name: a.Name, Website: a.Website, ClientId: a.ClientId, Scopes: a.Scopes.OAuthScopeString(), CreatedAt: a.CreatedAt, Tokens: n})
	}

	return out, nil
}

// DeleteApplication removes an application together with everything that
// could still authenticate through it: access and refresh tokens,
// authorization codes, user grants and pending Misskey sessions. The
// application row itself is soft-deleted so statuses that were posted
// through it keep their "via" reference. Everything happens in one
// transaction.
func DeleteApplication(conn *gorm.DB, id uint64) (tokens int64, err error) {
	err = conn.Transaction(func(tx *gorm.DB) error {
		var app OAuthApplication
		if err := tx.First(&app, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAppNotFound
			}

			return err
		}
		res := tx.Where("application_id = ?", id).Delete(&models.AuthToken{})
		if res.Error != nil {
			return res.Error
		}
		tokens = res.RowsAffected
		if err := tx.Where("application_id = ?", id).Delete(&OAuthAuthorizationCode{}).Error; err != nil {
			return err
		}
		if err := tx.Where("application_id = ?", id).Delete(&models.ApplicationUserAuth{}).Error; err != nil {
			return err
		}
		if tx.Migrator().HasTable(&models.MisskeyAuthSession{}) {
			if err := tx.Where("application_id = ?", id).Delete(&models.MisskeyAuthSession{}).Error; err != nil {
				return err
			}
		}

		return tx.Delete(&models.Application{}, id).Error
	})

	return tokens, err
}
