package models

import (
	"strings"

	"github.com/pkg/errors"
	"gorm.io/gorm"
)

// FindUserForLogin resolves what someone typed into a login form: a username
// (exact, then case-insensitive when that is unambiguous) or an email address
// (as stored, then lower-cased, since only a hash of the address is kept).
func FindUserForLogin(tx *gorm.DB, identifier string) (*User, error) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return nil, gorm.ErrRecordNotFound
	}

	user := &User{}

	if strings.Contains(identifier, "@") {
		hashes := []string{GetEmailHash(identifier)}
		if lower := strings.ToLower(identifier); lower != identifier {
			hashes = append(hashes, GetEmailHash(lower))
		}

		for _, h := range hashes {
			err := tx.Where("email_hash = ?", h).First(user).Error
			if err == nil {
				return user, nil
			}

			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
		}
	}

	err := tx.Where("username = ?", identifier).First(user).Error
	if err == nil || !errors.Is(err, gorm.ErrRecordNotFound) {
		return user, err
	}

	var matches []User
	if err := tx.Where("LOWER(username) = ?", strings.ToLower(identifier)).Limit(2).Find(&matches).Error; err != nil {
		return nil, err
	}

	if len(matches) != 1 {
		return nil, gorm.ErrRecordNotFound
	}

	return &matches[0], nil
}
