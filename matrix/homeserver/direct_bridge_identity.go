package homeserver

import (
	"errors"
	"strings"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

var errUnknownBridgeAccount = errors.New("unknown ActivityPub user")

func (hs *HomeServer) readableBridgeAccountID(a models.Account) string {
	return "@" + models.NormalizedMatrixAlias(a) + ":" + hs.Config.ServerName
}

// resolveBridgeAccount resolves indexed readable identities. Ambiguous remote
// handles and collisions with local accounts fail closed, never impersonating
// another actor. Ordinary Matrix IDs still return nil,nil.
func (hs *HomeServer) resolveBridgeAccount(tx *gorm.DB, userID string) (*models.Account, error) {
	local, server, ok := ParseMatrixUserID(userID)
	if !ok || server != hs.Config.ServerName || !strings.HasSuffix(local, "_ap") {
		return nil, nil
	}
	var accounts []models.Account
	if err := tx.Unscoped().Where("matrix_alias = ?", local).Limit(2).Find(&accounts).Error; err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, errUnknownBridgeAccount
	}
	if len(accounts) > 1 {
		return nil, models.ErrMatrixAliasCollision
	}
	a := accounts[0]
	if a.Domain == nil {
		return nil, nil
	}
	if a.IsBan || a.DeletedAt.Valid || len(userID) > 255 || hs.bridgeAccountID(a) != userID {
		return nil, errUnknownBridgeAccount
	}
	return &a, nil
}

func (hs *HomeServer) validateBridgeAccountIdentity(tx *gorm.DB, a models.Account) error {
	if a.Domain == nil {
		return nil
	}
	id := hs.bridgeAccountID(a)
	if len(id) > 255 {
		return errUnknownBridgeAccount
	}
	resolved, err := hs.resolveBridgeAccount(tx, id)
	if err != nil {
		return err
	}
	if resolved == nil || resolved.Id != a.Id {
		return errUnknownBridgeAccount
	}
	return nil
}
