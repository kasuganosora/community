package datatransfer

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/twofactor"
	"gorm.io/gorm"
)

func (i *importer) importAccounts() error {
	credentials := map[string]Credential{}
	for _, c := range i.bundle.Credentials {
		credentials[c.AccountID] = c
	}
	for _, a := range i.bundle.Accounts {
		if i.personal() && a.ID != i.bundle.OwnerID && !i.options.VerifiedReferences[a.ID] {
			if err := i.preserve("account", a.ID, a, "personal_unverified_account"); err != nil {
				return err
			}
			continue
		}
		_, err := i.apply("account", a.ID, accountReceiptValue(a, i.bundle, i.options), func(tx *gorm.DB) (uint64, error) {
			if i.personal() && a.ID == i.bundle.OwnerID {
				return i.options.OwnerAccountID, nil
			}
			var existing models.Account
			lookup := tx.Where("origin_id = ? OR local_actor_uri = ?", a.ActorURI, a.ActorURI).First(&existing).Error
			if lookup == nil {
				if a.Local && !i.personal() {
					return 0, errors.New("source local identity already exists without an import mapping")
				}
				return existing.Id, nil
			}
			if !errors.Is(lookup, gorm.ErrRecordNotFound) {
				return 0, lookup
			}
			if i.personal() {
				var candidates []models.Account
				if err := tx.Where("domain IS NULL AND username = ?", a.Username).Find(&candidates).Error; err != nil {
					return 0, err
				}
				for _, candidate := range candidates {
					if candidate.ActorURI(i.options.TargetBaseURL) == a.ActorURI {
						return candidate.Id, nil
					}
				}
			}
			local := a.Local && !i.personal()
			target := models.Account{Username: a.Username, DisplayName: a.DisplayName, Note: a.Note, Locked: a.Locked, Bot: a.Bot, Discoverable: a.Discoverable, NoIndex: a.NoIndex, HideCollections: a.HideCollections, IsBan: a.Suspended, CreatedAt: timePointer(a.CreatedAt), UpdatedAt: timePointer(a.UpdatedAt)}
			if local {
				target.LocalActorURI = a.ActorURI
				target.Acct = a.Username + "@__LOCAL__"
				if a.NoteHTML {
					target.Note = htmlsafe.StripTags(a.Note)
					target.ImportedNoteHTML = htmlsafe.Sanitize(a.Note)
				}
				// Misskey requireSigninToViewContents maps 1:1 to the account setting.
				target.RequireSigninToView = policyEnabled(a.Settings["requireSigninToViewContents"])
				if !a.System {
					c := credentials[a.ID]
					user := models.User{UUID: uuid.NewString(), UserName: a.Username, IsAdmin: a.Admin, IsBan: a.Suspended, EmailHash: models.GetEmailHash(c.Email), IsMailVerified: c.EmailVerified, CreatedAt: timePointer(a.CreatedAt), UpdatedAt: timePointer(a.UpdatedAt)}
					if c.Email == "" {
						user.EmailHash = "no-email:" + uuid.NewString()
					}
					if c.Disabled {
						user.Status = models.UserStatusRejected
					}
					if err := tx.Create(&user).Error; err != nil {
						return 0, err
					}
					target.UserId = user.Id
				}
			} else {
				domain := a.Domain
				if domain == "" {
					u, _ := url.Parse(a.ActorURI)
					domain = u.Host
				}
				// Imported remote preferences are hints, never a fresh verification.
				// Old archives have no three-state consent; retain explicit legacy
				// opt-ins/refusals but require a canonical actor refresh before use.
				target.RemoteDiscoverable = a.RemoteDiscoverable
				target.RemoteIndexable = a.RemoteIndexable
				target.RemoteHideCollections = a.RemoteHideCollections
				if target.RemoteDiscoverable == nil && a.Discoverable {
					target.RemoteDiscoverable = &a.Discoverable
				}
				if target.RemoteIndexable == nil && a.NoIndex {
					denied := false
					target.RemoteIndexable = &denied
				}
				if target.RemoteHideCollections == nil && a.HideCollections {
					target.RemoteHideCollections = &a.HideCollections
				}
				if target.RemoteDiscoverable != nil {
					target.Discoverable = *target.RemoteDiscoverable
				}
				if target.RemoteIndexable != nil {
					target.NoIndex = !*target.RemoteIndexable
				}
				if target.RemoteHideCollections != nil {
					target.HideCollections = *target.RemoteHideCollections
				}
				target.RemoteDiscoverySource = "import"
				target.Domain = &domain
				target.OriginId = a.ActorURI
				target.Acct = a.Username + "@" + domain
				if a.NoteHTML {
					target.Note = htmlsafe.Sanitize(a.Note)
				}
			}
			target.MatrixAlias = models.NormalizedMatrixAlias(target)
			if err := tx.Create(&target).Error; err != nil {
				return 0, err
			}
			extra := models.AccountExtra{AccountId: target.Id}
			for _, f := range a.Fields {
				extra.Fields = append(extra.Fields, models.AccountField{Name: f.Name, Val: f.Value, VerifiedAt: f.VerifiedAt})
			}
			if err := tx.Create(&extra).Error; err != nil {
				return 0, err
			}
			if local && target.UserId != 0 && len(a.Settings) > 0 {
				if err := tx.Create(&models.UserSetting{ID: uuid.NewString(), UserID: int64(target.UserId), Data: a.Settings}).Error; err != nil {
					return 0, err
				}
			}
			return target.Id, nil
		})
		if err != nil {
			return err
		}
	}
	for _, c := range i.bundle.Credentials {
		tf, reason := parseCredentialTwoFactor(c)
		if reason != "" {
			return errors.New("unsupported credential two-factor state: " + reason)
		}
		// The receipt digest covers the redacted form; the plaintext
		// secret only flows into the encrypted user_two_factors row.
		_, err := i.apply("credential", c.AccountID, redactTwoFactor(c), func(tx *gorm.DB) (uint64, error) {
			id, err := i.ref("account", c.AccountID)
			if err != nil {
				return 0, err
			}
			var account models.Account
			if err = tx.First(&account, id).Error; err != nil {
				return 0, err
			}
			if account.UserId == 0 {
				return 0, errors.New("credential has no login identity")
			}
			salt := c.PasswordSalt
			if salt == "" {
				salt = uuid.NewString() + uuid.NewString()
			}
			secret := models.UserSecretData{UserId: account.UserId, Password: c.PasswordHash, PasswordScheme: c.PasswordScheme, Salt: salt, Email: c.Email}
			// Skip historical event hooks, but explicitly retain target-side encryption.
			if err = secret.BeforeSave(tx); err != nil {
				return 0, err
			}
			if err = tx.Create(&secret).Error; err != nil {
				return 0, err
			}
			if tf != nil && tf.TOTP {
				if err = twofactor.ImportSecret(tx, account.UserId, tf.Secret, tf.BackupCodes); err != nil {
					return 0, errors.New("two-factor secret could not be stored")
				}
				i.report.Created["two_factor"]++
			}
			return account.UserId, nil
		})
		if err != nil {
			return err
		}
	}
	primary := map[string]bool{}
	for _, key := range i.bundle.Keys {
		if key.Primary && !key.Revoked && (key.ExpiresAt == nil || key.ExpiresAt.After(time.Now())) {
			primary[key.AccountID] = true
		}
	}
	chosen := map[string]bool{}
	for _, k := range i.bundle.Keys {
		if !i.sourceAccount(k.AccountID).Local {
			if err := i.preserve("remote_key", k.AccountID+"/"+k.KeyID, k, ""); err != nil {
				return err
			}
			continue
		}
		_, err := i.apply("key", k.AccountID+"\n"+k.KeyID, k, func(tx *gorm.DB) (uint64, error) {
			id, err := i.ref("account", k.AccountID)
			if err != nil {
				return 0, err
			}
			target := models.AccountFederationKey{AccountID: id, KeyID: k.KeyID, KeyIDHash: digestBytes([]byte(k.KeyID)), PublicKey: k.PublicKey, PrivateKey: k.PrivateKey, Algorithm: k.Algorithm, Revoked: k.Revoked, ExpiresAt: k.ExpiresAt}
			if err = tx.Create(&target).Error; err != nil {
				return 0, err
			}
			if !k.Revoked && (k.ExpiresAt == nil || k.ExpiresAt.After(time.Now())) && (k.Primary || !primary[k.AccountID] && !chosen[k.AccountID]) {
				selected := models.AccountKey{AccountID: id, KeyID: k.KeyID, PublicKey: k.PublicKey, PrivateKey: k.PrivateKey}
				if err = tx.Create(&selected).Error; err != nil {
					return 0, err
				}
			}
			return target.ID, nil
		})
		if err != nil {
			return err
		}
		if !k.Revoked && (k.ExpiresAt == nil || k.ExpiresAt.After(time.Now())) && (k.Primary || !primary[k.AccountID]) {
			chosen[k.AccountID] = true
		}
	}
	return nil
}
func (i *importer) importAccountImages() error {
	for _, a := range i.bundle.Accounts {
		if i.personal() && a.ID != i.bundle.OwnerID {
			continue
		}
		updates := map[string]any{}
		for _, entry := range []struct{ id, field, remote string }{{a.AvatarID, "avatar", a.AvatarURL}, {a.HeaderID, "header", a.HeaderURL}} {
			if entry.id != "" {
				id, err := i.ref("media", entry.id)
				if err != nil {
					return err
				}
				var media models.MediaAttachment
				if err = i.tx.First(&media, id).Error; err != nil {
					return err
				}
				path := media.RemoteUrl
				if media.FilePath != "" {
					path = strings.TrimRight(i.options.TargetBaseURL, "/") + fmt.Sprintf("/media/%d/%s", media.Id, url.PathEscape(media.FileName))
				}
				updates[entry.field] = path
				updates[entry.field+"_static"] = path
			} else if entry.remote != "" && safeHTTPURL(entry.remote) {
				updates[entry.field] = entry.remote
				updates[entry.field+"_static"] = entry.remote
			}
		}
		if i.personal() && (a.Username != "" || a.ActorURI != "" || a.DisplayName != "" || a.Note != "") {
			updates["display_name"] = a.DisplayName
			updates["note"] = a.Note
			updates["imported_note_html"] = ""
			updates["locked"] = a.Locked
			updates["bot"] = a.Bot
			updates["discoverable"] = a.Discoverable
			updates["no_index"] = a.NoIndex
			updates["hide_collections"] = a.HideCollections
			if a.NoteHTML {
				updates["note"] = htmlsafe.StripTags(a.Note)
				updates["imported_note_html"] = htmlsafe.Sanitize(a.Note)
			}
		}
		if len(updates) > 0 {
			_, err := i.apply("profile", a.ID, a, func(tx *gorm.DB) (uint64, error) {
				id, err := i.ref("account", a.ID)
				if err != nil {
					return 0, err
				}
				if err = tx.Model(&models.Account{}).Where("id = ?", id).Updates(updates).Error; err != nil {
					return 0, err
				}
				if i.personal() {
					fields := make([]models.AccountField, 0, len(a.Fields))
					for _, f := range a.Fields {
						fields = append(fields, models.AccountField{Name: f.Name, Val: f.Value})
					}
					extra := models.AccountExtra{AccountId: id}
					if err = tx.Where("account_id = ?", id).FirstOrCreate(&extra).Error; err != nil {
						return 0, err
					}
					extra.Fields = fields
					if err = tx.Model(&extra).Select("fields").Updates(&extra).Error; err != nil {
						return 0, err
					}
					if len(a.Settings) > 0 {
						var settings models.UserSetting
						err = tx.Where("user_id = ?", i.options.OwnerUserID).First(&settings).Error
						if errors.Is(err, gorm.ErrRecordNotFound) {
							settings = models.UserSetting{ID: uuid.NewString(), UserID: int64(i.options.OwnerUserID), Data: a.Settings}
							err = tx.Create(&settings).Error
						} else if err == nil {
							settings.Data = a.Settings
							err = tx.Model(&settings).Select("data").Updates(&settings).Error
						}
						if err != nil {
							return 0, err
						}
					}
				}
				return id, nil
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func sourceDomain(a Account) *string {
	if a.Local {
		return nil
	}
	d := a.Domain
	if d == "" {
		u, _ := url.Parse(a.ActorURI)
		d = u.Host
	}
	d = strings.ToLower(d)
	return &d
}
