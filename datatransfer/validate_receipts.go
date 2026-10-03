package datatransfer

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

func accountReceiptValue(a Account, b *Bundle, o Options) any {
	if o.Scope == "personal" && a.ID != b.OwnerID && o.VerifiedReferences[a.ID] {
		return struct{ ID, ActorURI string }{a.ID, a.ActorURI}
	}
	return a
}

func validateExistingReceipts(ctx context.Context, conn *gorm.DB, b *Bundle, o Options, r *Report) error {
	if !conn.Migrator().HasTable(&ImportReceipt{}) {
		return nil
	}
	namespace := transferNamespace(b, o)
	check := func(kind, id string, value any) error {
		var receipt ImportReceipt
		err := conn.WithContext(ctx).Where("namespace = ? AND kind = ? AND source_hash = ?", namespace, kind, digestBytes([]byte(id))).First(&receipt).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		digest, err := digestValue(value)
		if err != nil {
			return err
		}
		if receipt.Digest != digest || receipt.SourceID != id {
			issue(r, "source_conflict", kind, id, "source identifier was previously imported with different content")
		}
		return nil
	}
	for _, a := range b.Accounts {
		if err := check("account", a.ID, accountReceiptValue(a, b, o)); err != nil {
			return err
		}
	}
	for _, c := range b.Credentials {
		if err := check("credential", c.AccountID, c); err != nil {
			return err
		}
	}
	for _, k := range b.Keys {
		if err := check("key", k.AccountID+"\n"+k.KeyID, k); err != nil {
			return err
		}
	}
	for _, p := range b.Posts {
		if err := check("post", p.ID, p); err != nil {
			return err
		}
	}
	for _, m := range b.Media {
		if err := check("media", m.ID, m); err != nil {
			return err
		}
	}
	for _, l := range b.Lists {
		if err := check("list", l.ID, l); err != nil {
			return err
		}
	}
	for _, p := range b.Polls {
		if err := check("poll", p.ID, p); err != nil {
			return err
		}
	}
	for _, e := range b.Emojis {
		if err := check("emoji", e.ID, e); err != nil {
			return err
		}
	}
	return nil
}
