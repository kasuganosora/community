package homeserver

import (
	"context"
	"errors"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A reply follows its trusted parent mapping, never a client-supplied room ID
// or a union of participants seen in earlier messages.
func (hs *HomeServer) explicitBridgeRoomForStatus(ctx context.Context, status *models.Status, participants map[string]models.Account) (string, string, map[string]models.Account, bool, error) {
	if status.InReplyToId == nil {
		return "", "", participants, false, nil
	}
	conn := db.GetDB().WithContext(ctx)
	var mapping models.MatrixDMDelivery
	err := conn.Where("status_id = ? AND state = ? AND room_id <> ''", *status.InReplyToId, "delivered").First(&mapping).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", participants, false, nil
	}
	if err != nil {
		return "", "", nil, false, err
	}
	var conv BridgeConversation
	err = conn.Where("room_id = ?", mapping.RoomID).First(&conv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", "", participants, false, nil
	}
	if err != nil {
		return "", "", nil, false, err
	}
	remapped, err := hs.validateInboundConversation(conn, &conv, status, participants)
	return conv.RoomID, conv.Controller, remapped, true, err
}

func (hs *HomeServer) validateInboundConversation(tx *gorm.DB, conv *BridgeConversation, status *models.Status, participants map[string]models.Account) (map[string]models.Account, error) {
	if conv.State != "active" {
		return nil, errors.New("conversation membership transition pending")
	}
	var room MatrixRoom
	if err := tx.Where("room_id = ?", conv.RoomID).First(&room).Error; err != nil {
		return nil, err
	}
	if room.Creator != conv.Controller {
		return nil, errDirectMessageSkipped
	}
	var members []BridgeConversationMember
	if err := tx.Where("conversation_id = ? AND left_revision = 0", conv.ID).Find(&members).Error; err != nil {
		return nil, err
	}
	accounts := map[uint64]models.Account{}
	for _, account := range participants {
		accounts[account.Id] = account
	}
	remapped := map[string]models.Account{}
	for _, member := range members {
		if account, ok := accounts[member.AccountID]; ok {
			remapped[member.MatrixID] = account
			delete(accounts, member.AccountID)
		}
	}
	if len(accounts) != 0 {
		return nil, errDirectMessageSkipped
	}
	var joined []MatrixRoomMember
	if err := tx.Where("room_id = ? AND membership = ?", conv.RoomID, MembershipJoin).Find(&joined).Error; err != nil {
		return nil, err
	}
	senderPresent := false
	for _, member := range joined {
		if member.UserID == conv.Controller {
			continue
		}
		account, ok := remapped[member.UserID]
		if !ok {
			return nil, errDirectMessageSkipped
		}
		if account.Id == status.AccountId {
			senderPresent = true
		}
	}
	if !senderPresent {
		return nil, errDirectMessageSkipped
	}
	return remapped, nil
}

// Recheck inside the Matrix event transaction under the same conversation row
// lock used for member changes. Routing alone would leave a join/send race.
func (hs *HomeServer) validateExplicitBridgeEvent(tx *gorm.DB, roomID string, statusID uint64) error {
	var conv BridgeConversation
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("room_id = ?", roomID).First(&conv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var status models.Status
	if err := tx.Preload("Account").Preload("ToAccounts").First(&status, statusID).Error; err != nil {
		return err
	}
	if status.Visibility != models.StatusVisibilityDirect || status.ScheduledAt != nil || status.Account == nil || status.Account.IsBan {
		return errDirectMessageSkipped
	}
	if err := hs.validateBridgeAccountIdentity(tx, *status.Account); err != nil {
		return errDirectMessageSkipped
	}
	participants := map[string]models.Account{hs.bridgeAccountID(*status.Account): *status.Account}
	for _, account := range status.ToAccounts {
		if account.IsBan {
			return errDirectMessageSkipped
		}
		blocked, err := models.AccountsBlocked(tx, status.Account, account)
		if err != nil {
			return err
		}
		if blocked {
			return errDirectMessageSkipped
		}
		if err := hs.validateBridgeAccountIdentity(tx, *account); err != nil {
			return errDirectMessageSkipped
		}
		participants[hs.bridgeAccountID(*account)] = *account
	}
	_, err = hs.validateInboundConversation(tx, &conv, &status, participants)
	return err
}
