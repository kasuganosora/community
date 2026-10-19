package datatransfer

import (
	"errors"
	"fmt"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// systemRoleSetting maps an Account.SystemRole to the setting naming it.
var systemRoleSetting = map[string]string{
	"instance_actor": models.SettingInstanceActorURI,
	"relay_actor":    models.SettingRelayActorURI,
}

// importFederation designates imported system actors and restores relay
// subscriptions. Misskey's instance.actor and relay.actor are imported as
// local accounts keeping their actor URI (<base>/users/<id>) and keypair;
// the settings make the server serve and sign with them as its instance and
// relay actor, so remote caches of the keyId stay valid and relays keep
// recognising the subscription.
func (i *importer) importFederation() error {
	if i.personal() {
		return nil
	}
	for _, a := range i.bundle.Accounts {
		key := systemRoleSetting[a.SystemRole]
		if key == "" || !a.Local || a.ActorURI == "" {
			continue
		}
		if i.ids["account"][a.ID] == 0 {
			continue
		}
		var existing models.SystemSetting
		err := i.tx.Where(clause.Eq{Column: clause.Column{Name: "key"}, Value: key}).First(&existing).Error
		switch {
		case err == nil:
			if v, _ := existing.Val.(string); v != a.ActorURI {
				i.report.Issues = append(i.report.Issues, Issue{Severity: "warning", Code: "system_actor_conflict", Kind: "account", SourceID: a.ID,
					Message: fmt.Sprintf("%s is already set to another actor; the imported %s (%s) was not designated", key, a.SystemRole, a.ActorURI)})
			}
		case errors.Is(err, gorm.ErrRecordNotFound):
			if err := i.tx.Create(&models.SystemSetting{Key: key, Val: a.ActorURI}).Error; err != nil {
				return err
			}
			i.report.Created["system_actor"]++
		default:
			return err
		}
	}
	for _, r := range i.bundle.Relays {
		if r.Inbox == "" {
			continue
		}
		if !i.conn().Migrator().HasTable(&models.Relay{}) {
			i.report.Issues = append(i.report.Issues, Issue{Severity: "warning", Code: "relay_not_imported", Kind: "relay", SourceID: r.ID, Message: "target has no relays table; run migrations first"})
			continue
		}
		var existing models.Relay
		err := i.tx.Unscoped().Where("inbox_url = ?", r.Inbox).First(&existing).Error
		if err == nil {
			i.report.Skipped["relay"]++
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		state := models.RelayStateIdle
		switch r.State {
		case "accepted":
			state = models.RelayStateAccepted
		case "requesting":
			state = models.RelayStatePending
		case "rejected":
			state = models.RelayStateRejected
		}
		relay := models.Relay{InboxURL: r.Inbox, FollowActivityID: r.FollowID, State: state, ActorURI: r.ActorURI}
		if err := i.tx.Create(&relay).Error; err != nil {
			return err
		}
		i.report.Created["relay"]++
	}
	return nil
}

func (i *importer) conn() *gorm.DB { return i.tx }
