package newacctmod

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrAppealExists   = errors.New("appeal already submitted for this case")
	ErrEmptyAppeal    = errors.New("appeal body is required")
	ErrAppealNotFound = errors.New("appeal not found")
	ErrAppealClosed   = errors.New("appeal already resolved")
)

func CreateAppeal(conn *gorm.DB, accountID, statusID uint64, body string) (*Appeal, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return nil, ErrEmptyAppeal
	}
	key := SuspensionKeyFor(accountID, statusID)
	a := &Appeal{
		AccountID:     accountID,
		SuspensionKey: key,
		Body:          body,
		StatusID:      statusID,
		State:         "open",
	}
	err := conn.Clauses(clause.OnConflict{DoNothing: true}).Create(a).Error
	if err != nil {
		return nil, err
	}
	if a.ID == 0 {
		return nil, ErrAppealExists
	}
	return a, nil
}

// ResolveAppeal marks an open appeal accepted or rejected.
// Restoring login access is left to the existing admin account action.
func ResolveAppeal(conn *gorm.DB, appealID, adminAccountID uint64, accept bool, note string) (*Appeal, error) {
	var a Appeal
	err := conn.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&a, appealID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAppealNotFound
			}
			return err
		}
		if a.State != "open" {
			return ErrAppealClosed
		}
		now := time.Now()
		a.ResolvedAt = &now
		a.ResolvedByID = adminAccountID
		a.AdminNote = strings.TrimSpace(note)
		if accept {
			a.State = "accepted"
		} else {
			a.State = "rejected"
		}
		return tx.Save(&a).Error
	})
	if err != nil {
		return nil, err
	}
	return &a, nil
}
