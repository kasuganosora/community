package homeserver

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// MatrixExternalActor is an explicit identity mapping, not a login account.
// Only authenticated federation events admitted to a mixed conversation may
// send as this actor; callers must check room membership before using it.
type MatrixExternalActor struct {
	MatrixID  string  `gorm:"primaryKey;size:255"`
	AccountID *uint64 `gorm:"uniqueIndex"`
}

func (MatrixExternalActor) TableName() string { return "matrix_external_actors" }

func (hs *HomeServer) EnsureExternalMatrixActor(tx *gorm.DB, matrixID string) (*models.Account, error) {
	local, domain, ok := ParseMatrixUserID(matrixID)
	if !ok || local == "" || len(matrixID) > 255 || strings.EqualFold(domain, hs.Config.ServerName) || strings.ContainsAny(matrixID, "<>\"/\\?#") || strings.IndexFunc(matrixID, unicode.IsSpace) >= 0 || strings.IndexFunc(matrixID, unicode.IsControl) >= 0 {
		return nil, errors.New("invalid external Matrix identity")
	}
	u, err := url.Parse("https://" + domain)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid Matrix server name")
	}
	var account models.Account
	err = tx.Transaction(func(conn *gorm.DB) error {
		mapping := MatrixExternalActor{MatrixID: matrixID}
		if err := conn.Clauses(clause.OnConflict{DoNothing: true}).Create(&mapping).Error; err != nil {
			return err
		}
		if err := conn.Clauses(clause.Locking{Strength: "UPDATE"}).First(&mapping, "matrix_id = ?", matrixID).Error; err != nil {
			return err
		}
		if mapping.AccountID != nil {
			if err := conn.First(&account, *mapping.AccountID).Error; err != nil {
				return err
			}
			if account.Domain != nil || account.UserId != 0 || !account.Bot || account.IsBan {
				return errors.New("external Matrix actor unavailable")
			}
			return nil
		}
		sum := sha256.Sum256([]byte(matrixID))
		username := "_mx_" + hex.EncodeToString(sum[:])[:56]
		var occupied int64
		if err := conn.Model(&models.Account{}).Where("username = ? AND domain IS NULL", username).Count(&occupied).Error; err != nil {
			return err
		}
		if occupied != 0 {
			return errors.New("external Matrix actor namespace conflict")
		}
		display := []rune(matrixID + " (Matrix)")
		if len(display) > 128 {
			display = append(display[:119], []rune(" (Matrix)")...)
		}
		account = models.Account{Username: username, DisplayName: string(display), Bot: true, NoIndex: true, HideCollections: true, Locked: true,
			Note: fmt.Sprintf("Matrix identity: %s\nThis account represents this external Matrix user in Community mixed conversations. It is not a Community login account.", matrixID)}
		if err := conn.Create(&account).Error; err != nil {
			return err
		}
		return conn.Model(&mapping).Update("account_id", account.Id).Error
	})
	if err != nil {
		return nil, err
	}
	return &account, nil
}
