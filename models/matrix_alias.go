package models

import (
	"errors"
	"strings"

	"gorm.io/gorm"
)

var ErrMatrixAliasCollision = errors.New("Matrix account name is ambiguous")

// NormalizedMatrixAlias is the public Matrix localpart. Punctuation replacement
// is deliberately readable, so callers must reject ambiguous account mappings.
func NormalizedMatrixAlias(a Account) string {
	if a.Domain == nil {
		return strings.ToLower(a.Username)
	}
	handle := strings.ToLower(a.Username + "@" + *a.Domain)
	var label strings.Builder
	for _, c := range []byte(handle) {
		switch {
		case c == '@' || c == '.':
			label.WriteByte('_')
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '_', c == '-':
			label.WriteByte(c)
		default:
			const hex = "0123456789abcdef"
			label.WriteByte('=')
			label.WriteByte(hex[c>>4])
			label.WriteByte(hex[c&15])
		}
	}
	return label.String() + "_ap"
}

// BackfillMatrixAliases runs after schema migration. Indexed aliases avoid an
// account-table scan whenever a Matrix client looks up or invites an AP user.
func BackfillMatrixAliases(tx *gorm.DB) error {
	var after uint64
	for {
		var accounts []Account
		if err := tx.Unscoped().Select("id", "username", "domain").Where("id > ? AND (matrix_alias = '' OR matrix_alias IS NULL)", after).Order("id ASC").Limit(500).Find(&accounts).Error; err != nil {
			return err
		}
		if len(accounts) == 0 {
			return nil
		}
		for _, a := range accounts {
			if err := tx.Unscoped().Model(&Account{}).Where("id = ?", a.Id).UpdateColumn("matrix_alias", NormalizedMatrixAlias(a)).Error; err != nil {
				return err
			}
		}
		after = accounts[len(accounts)-1].Id
	}
}
