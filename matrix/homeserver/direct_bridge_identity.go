package homeserver

import (
	"errors"
	"strconv"
	"strings"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

var errUnknownBridgeAccount = errors.New("unknown ActivityPub user")

// readableBridgeAccountID retains the account handle in the ID. The short,
// indexed account suffix makes punctuation replacement collision-safe and
// lets incoming Matrix invitations resolve an account without table scans.
func (hs *HomeServer) readableBridgeAccountID(a models.Account) string {
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
	suffix := "=" + strconv.FormatUint(a.Id, 36) + ":" + hs.Config.ServerName
	// Matrix IDs are at most 255 bytes, including the sigil and server name.
	budget := 255 - len("@_ap_") - len(suffix)
	name := label.String()
	if budget < 0 {
		budget = 0
	}
	if len(name) > budget {
		name = name[:budget]
	}
	return "@_ap_" + name + suffix
}

// resolveBridgeAccount only handles our reserved ghost namespace. Ordinary
// Matrix IDs return nil,nil; malformed or unknown ghosts return an error.
func (hs *HomeServer) resolveBridgeAccount(tx *gorm.DB, userID string) (*models.Account, error) {
	local, server, ok := ParseMatrixUserID(userID)
	if !ok || server != hs.Config.ServerName || !models.IsReservedBridgeUsername(local) {
		return nil, nil
	}
	separator := strings.LastIndexByte(local, '=')
	if separator < len("_ap_") {
		return nil, errUnknownBridgeAccount
	}
	id, err := strconv.ParseUint(local[separator+1:], 36, 64)
	if err != nil || id == 0 {
		return nil, errUnknownBridgeAccount
	}
	var account models.Account
	if err := tx.First(&account, id).Error; err != nil {
		return nil, err
	}
	if account.Domain == nil || account.IsBan || hs.bridgeAccountID(account) != userID {
		return nil, errUnknownBridgeAccount
	}
	return &account, nil
}
