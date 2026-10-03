package models

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var reactionSchemaRequired atomic.Bool

func RequireReactionSchema()       { reactionSchemaRequired.Store(true) }
func ReactionSchemaRequired() bool { return reactionSchemaRequired.Load() }

var ErrInvalidReaction = errors.New("reaction is unavailable")
var ErrReactionForbidden = errors.New("cannot react to this status")

// The unique actor/status pair implements Misskey's one reaction per person.
// Counts are derived from rows rather than vulnerable read/modify/write counters.
type StatusReaction struct {
	ID          uint64    `gorm:"primaryKey;autoIncrement" json:"id"`
	AccountID   uint64    `gorm:"not null;uniqueIndex:idx_reaction_actor_status,priority:1" json:"account_id"`
	StatusID    uint64    `gorm:"not null;index;uniqueIndex:idx_reaction_actor_status,priority:2" json:"status_id"`
	Name        string    `gorm:"type:varchar(320);not null" json:"name"`
	EmojiID     uint64    `json:"emoji_id"`
	ActivityURI string    `gorm:"type:varchar(2048)" json:"activity_uri"`
	CreatedAt   time.Time `json:"created_at"`
}

// ValidUnicodeReaction allows a bounded emoji sequence, never arbitrary text,
// markup, invisible-only values or surrogate/invalid UTF-8 data.
func ValidUnicodeReaction(name string) bool {
	if !utf8.ValidString(name) || len(name) == 0 || len(name) > 64 {
		return false
	}
	base := false
	for _, r := range name {
		switch {
		case r >= 0x1f000 && r <= 0x1faff, r >= 0x2600 && r <= 0x27ff, r == 0x00a9, r == 0x00ae, r == 0x2122, r == 0x203c, r == 0x2049, r >= 0x2194 && r <= 0x21ff, r >= 0x2300 && r <= 0x23ff, r >= 0x2b00 && r <= 0x2bff, r == 0x3030, r == 0x303d, r == 0x3297, r == 0x3299:
			base = true
		case r == 0xfe0f, r == 0x200d, r == 0x20e3, r >= 0xe0020 && r <= 0xe007f:
		case r >= '0' && r <= '9' || r == '#' || r == '*':
			if !strings.ContainsRune(name, 0x20e3) {
				return false
			}
			base = true
		default:
			if unicode.IsSpace(r) || unicode.IsLetter(r) {
				return false
			}
			return false
		}
	}
	return base
}

func ResolveReaction(conn *gorm.DB, name string, actor *Account) (*Emoji, error) {
	if !strings.HasPrefix(name, ":") {
		if ValidUnicodeReaction(name) {
			return nil, nil
		}
		return nil, ErrInvalidReaction
	}
	if !strings.HasSuffix(name, ":") || strings.Contains(name, "@") {
		return nil, ErrInvalidReaction
	}
	var emoji Emoji
	if err := conn.Where("shortcode = ? AND domain = ? AND disabled = ?", strings.Trim(name, ":"), "", false).First(&emoji).Error; err != nil {
		return nil, ErrInvalidReaction
	}
	if len(emoji.ReactionRoleIDs) > 0 {
		if actor == nil || actor.Domain != nil {
			return nil, ErrInvalidReaction
		}
		var n int64
		if err := conn.Model(&RoleUser{}).Where("user_id = ? AND role_id IN ?", actor.UserId, emoji.ReactionRoleIDs).Count(&n).Error; err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, ErrInvalidReaction
		}
	}
	return &emoji, nil
}

// SetStatusReaction serializes writes on the parent status, covering concurrent
// insert/change/delete. The durable outbox is committed with each actual change.
func SetStatusReaction(conn *gorm.DB, statusID uint64, actor *Account, name string, emoji *Emoji, activityURI string, remove bool) error {
	ctx, publish := BeginReactionNotifications(conn.Statement.Context)
	err := conn.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var status Status
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Account").Preload("ToAccounts").First(&status, statusID).Error; err != nil {
			return err
		}
		allowed, err := status.CanViewEx(tx, actor)
		if err != nil {
			return err
		}
		if !allowed || actor.IsBan {
			return ErrReactionForbidden
		}
		if !remove && actor.Domain == nil {
			current, resolveErr := ResolveReaction(tx, name, actor)
			if resolveErr != nil {
				return resolveErr
			}
			emoji = current
		}
		if emoji != nil && emoji.LocalOnly && (actor.Domain != nil || !status.IsLocal()) {
			return ErrInvalidReaction
		}
		var old StatusReaction
		err = tx.Where("account_id = ? AND status_id = ?", actor.Id, statusID).First(&old).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if remove && (old.Name != name || (activityURI != "" && old.ActivityURI != activityURI)) {
				return nil
			}
			if !remove && old.Name == name {
				if actor.Domain != nil && activityURI != "" && old.ActivityURI != activityURI {
					return tx.Model(&StatusReaction{}).Where("id = ?", old.ID).UpdateColumn("activity_uri", activityURI).Error
				}
				return nil
			}
			if err = queueReactionEvent(tx, "ReactionUndo", &old, &status, actor, nil); err != nil {
				return err
			}
			if err = deleteReactionNotification(tx, actor.Id, statusID); err != nil {
				return err
			}
			if err = tx.Delete(&old).Error; err != nil {
				return err
			}
		}
		if remove {
			return nil
		}
		row := StatusReaction{StatusID: statusID, AccountID: actor.Id, Name: name, ActivityURI: activityURI}
		if emoji != nil {
			row.EmojiID = emoji.Id
		}
		if err = tx.Create(&row).Error; err != nil {
			return err
		}
		if err = createReactionNotification(tx, &row, &status, actor); err != nil {
			return err
		}
		return queueReactionEvent(tx, "Reaction", &row, &status, actor, emoji)
	})
	if err == nil {
		publish()
	}
	return err
}

type ReactionSnapshot struct {
	Reaction      StatusReaction
	ObjectURI     string
	OwnerUsername string
	Emoji         *Emoji
}

func queueReactionEvent(tx *gorm.DB, kind string, reaction *StatusReaction, status *Status, actor *Account, emoji *Emoji) error {
	if actor.Domain != nil || !hasFederationOutbox(tx) {
		return nil
	}
	if emoji == nil && reaction.EmojiID != 0 {
		var stored Emoji
		if err := tx.Unscoped().First(&stored, reaction.EmojiID).Error; err != nil {
			return err
		}
		emoji = &stored
	}
	if emoji != nil && emoji.LocalOnly {
		return nil
	}
	// Reactions are delivered to the object owner, who handles distribution.
	if status.Account == nil {
		return fmt.Errorf("reaction target has no owner")
	}
	objectURI := ""
	recipients := []uint64{}
	if status.Account.Domain != nil {
		if status.OriginUri == nil || *status.OriginUri == "" {
			return fmt.Errorf("remote reaction target has no canonical URI")
		}
		objectURI = *status.OriginUri
		recipients = append(recipients, status.AccountId)
	} else {
		var targets []Account
		if err := tx.Model(&Account{}).Where("domain IS NOT NULL AND (id IN (SELECT account_id FROM follows WHERE follow_id = ? AND status = ?) OR id IN (SELECT account_id FROM status_to_accounts WHERE status_id = ?))", status.AccountId, FollowStatusFollowed, status.Id).Find(&targets).Error; err != nil {
			return err
		}
		for i := range targets {
			ok, err := status.CanViewEx(tx, &targets[i])
			if err != nil {
				return err
			}
			if ok {
				recipients = append(recipients, targets[i].Id)
			}
		}
	}
	payload, err := json.Marshal(ReactionSnapshot{Reaction: *reaction, ObjectURI: objectURI, OwnerUsername: status.Account.Username, Emoji: emoji})
	if err != nil {
		return err
	}
	return tx.Create(&FederationOutbox{ObjectID: status.Id, AccountID: actor.Id, Kind: kind, Payload: string(payload), RecipientIDs: recipients}).Error
}
