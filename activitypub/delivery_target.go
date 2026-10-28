package activitypub

import (
	"errors"
	"fmt"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

const (
	// deliveryTargetCooldownStep is added to a target's accumulated cooldown
	// on every failed delivery or inbox resolution.
	deliveryTargetCooldownStep = 5 * time.Minute
	// deliveryTargetCooldownGrace is the accumulated cooldown that is stored
	// but not yet enforced. A total of exactly this long still attempts.
	deliveryTargetCooldownGrace = 60 * time.Minute
	// deliveryTargetCooldownMax is the most cooldown one target may store.
	// Further failures keep the window at this length instead of growing it.
	deliveryTargetCooldownMax = 24 * time.Hour
)

// errDeliveryTargetCooling marks an expand that did not contact a target
// because its cooldown window is open. It does not add another failure.
var errDeliveryTargetCooling = errors.New("skipped: delivery target cooling down")

// DeliveryTarget stores the cooldown for one federation delivery target.
// Target is the inbox URL, or the actor document URL when expand has not
// resolved an inbox yet. Cooldown is the accumulated duration in seconds:
// each failure adds five minutes, and the stored total never exceeds 24 hours.
// While that total is at most 60 minutes, CooldownUntil stays null and
// delivery still runs. Above that, CooldownUntil is the end of the skip
// window (the moment of the failure plus the accumulated cooldown, capped
// at 24 hours). A successful delivery sets Cooldown back to 0 and
// CooldownUntil to null.
type DeliveryTarget struct {
	ID            uint64     `gorm:"primaryKey;autoIncrement"`
	Target        string     `gorm:"type:varchar(255);not null;uniqueIndex"`
	Cooldown      int64      `gorm:"not null;default:0"`
	CooldownUntil *time.Time `gorm:"index"`
	UpdatedAt     *time.Time `gorm:"autoUpdateTime"`
}

func (DeliveryTarget) TableName() string { return "activitypub_delivery_targets" }

func deliveryTargetCooldownSeconds(d time.Duration) int64 {
	return int64(d / time.Second)
}

func deliveryTargetKey(account *models.Account) string {
	if account != nil && account.OriginId != "" {
		return account.OriginId
	}
	if account == nil {
		return ""
	}
	return fmt.Sprintf("acct:%d", account.Id)
}

// noteDeliveryTargetFailure adds one 5 minute step to target, stopping at 24
// hours. The skip window is recorded only once the accumulated cooldown is
// greater than 60 minutes.
func noteDeliveryTargetFailure(now time.Time, target string) error {
	if target == "" {
		return nil
	}
	var err error
	for range 2 {
		err = noteDeliveryTargetFailureOnce(now, target)
		if err == nil {
			return nil
		}
	}
	return err
}

func noteDeliveryTargetFailureOnce(now time.Time, target string) error {
	step := deliveryTargetCooldownSeconds(deliveryTargetCooldownStep)
	grace := deliveryTargetCooldownSeconds(deliveryTargetCooldownGrace)
	maxCooldown := deliveryTargetCooldownSeconds(deliveryTargetCooldownMax)
	return db.GetDB().Transaction(func(tx *gorm.DB) error {
		table := (DeliveryTarget{}).TableName()
		// CASE is portable across SQLite, MySQL and PostgreSQL. MIN/LEAST is not.
		res := tx.Exec("UPDATE "+table+" SET cooldown = CASE WHEN cooldown + ? > ? THEN ? ELSE cooldown + ? END WHERE target = ?", step, maxCooldown, maxCooldown, step, target)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			if err := tx.Create(&DeliveryTarget{Target: target, Cooldown: step}).Error; err != nil {
				return err
			}
		}
		var row DeliveryTarget
		if err := tx.Where("target = ?", target).First(&row).Error; err != nil {
			return err
		}
		if row.Cooldown > grace {
			until := now.Add(time.Duration(row.Cooldown) * time.Second)
			return tx.Model(&DeliveryTarget{}).Where("target = ?", target).Update("cooldown_until", until).Error
		}
		return tx.Model(&DeliveryTarget{}).Where("target = ?", target).Update("cooldown_until", gorm.Expr("NULL")).Error
	})
}

// deliveryTargetCooling reports whether target must be skipped at now.
// A missing row, an accumulated cooldown of at most 60 minutes, or a window
// that has already elapsed all mean the target is still attempted.
func deliveryTargetCooling(target string, now time.Time) (bool, time.Time, error) {
	if target == "" {
		return false, time.Time{}, nil
	}
	// Find, not First: a target with no row yet is the common case, and First
	// would log a record-not-found for every healthy inbox.
	var rows []DeliveryTarget
	if err := db.GetDB().Where("target = ?", target).Limit(1).Find(&rows).Error; err != nil {
		return false, time.Time{}, err
	}
	if len(rows) == 0 || !rows[0].cooling(now) {
		return false, time.Time{}, nil
	}
	return true, *rows[0].CooldownUntil, nil
}

func (row DeliveryTarget) cooling(now time.Time) bool {
	grace := deliveryTargetCooldownSeconds(deliveryTargetCooldownGrace)
	return row.Cooldown > grace && row.CooldownUntil != nil && row.CooldownUntil.After(now)
}

// clearDeliveryTargetCooldown drops the accumulated cooldown and the skip
// window for every named target. A successful delivery calls this immediately.
func clearDeliveryTargetCooldown(targets ...string) error {
	cleaned := make([]string, 0, len(targets))
	seen := map[string]bool{}
	for _, target := range targets {
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		cleaned = append(cleaned, target)
	}
	if len(cleaned) == 0 {
		return nil
	}
	return db.GetDB().Model(&DeliveryTarget{}).Where("target IN ?", cleaned).Updates(map[string]any{
		"cooldown":       0,
		"cooldown_until": gorm.Expr("NULL"),
	}).Error
}

func targetCoolingDown(target string) bool {
	cooling, _, err := deliveryTargetCooling(target, time.Now())
	if err != nil {
		log.Warn("Federation delivery target cooldown: %s: %v", target, err)
		return false
	}
	return cooling
}

// queueDelivery leaves a pending row in place, but pushes its next attempt
// out to the end of an open cooldown window so a cooled inbox is not due.
func queueDelivery(d Delivery) Delivery {
	if d.Status != DeliveryStatusPending || d.TargetInbox == "" {
		return d
	}
	cooling, until, err := deliveryTargetCooling(d.TargetInbox, time.Now())
	if err != nil {
		log.Warn("Federation delivery target cooldown: %s: %v", d.TargetInbox, err)
		return d
	}
	if cooling {
		d.NextRetryAt = &until
	}
	return d
}
