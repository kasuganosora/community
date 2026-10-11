package homeserver

import (
	"errors"
	"strings"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"golang.org/x/net/idna"
	"gorm.io/gorm"
)

func bridgePublicDisplayName(account models.Account) string {
	domain := *account.Domain
	if decoded, err := idna.Lookup.ToUnicode(domain); err == nil {
		domain = decoded
	}
	handle := account.Username + "@" + domain
	if account.DisplayName == "" {
		return handle
	}
	return account.DisplayName + " (" + handle + ")"
}

func (s *SearchService) directoryRemoteAccount(query string) (*models.Account, error) {
	query = strings.TrimSpace(query)
	if strings.HasPrefix(query, "@") && strings.Contains(query, ":") {
		account, err := s.hs.resolveBridgeAccount(db.GetDB(), query)
		// Invalid synthetic IDs are ordinary empty search results.
		if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, errUnknownBridgeAccount) {
			return nil, nil
		}
		return account, err
	}
	handle := strings.TrimPrefix(query, "@")
	index := strings.LastIndexByte(handle, '@')
	if index <= 0 || index == len(handle)-1 {
		return nil, nil
	}
	domain, err := idna.Lookup.ToASCII(handle[index+1:])
	if err != nil {
		return nil, nil
	}
	var account models.Account
	err = db.GetDB().Where("username = ? AND domain = ? AND is_ban = ?", handle[:index], strings.ToLower(domain), false).First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.hs.validateBridgeAccountIdentity(db.GetDB(), account); err != nil {
		return nil, nil
	}
	return &account, nil
}
