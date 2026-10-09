package datatransfer

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// ResolvePersonalReferences performs discovery only: it never creates database
// identities or emits federation actions. Source IDs remain stable references.
func ResolvePersonalReferences(ctx context.Context, conn *gorm.DB, bundle *Bundle) error {
	return resolvePersonalReferences(ctx, conn, bundle, func(ctx context.Context, a Account) (*activitypub.Actor, error) {
		if a.ActorURI != "" {
			return activitypub.NewFetchClient().FetchActor(ctx, a.ActorURI)
		}
		actor, err := activitypub.DiscoverActorContext(ctx, a.Username+"@"+a.Domain)
		if err != nil {
			return nil, err
		}
		canonical, err := activitypub.NewFetchClient().FetchActor(ctx, actor.ID)
		if err == nil && canonical.ID != actor.ID {
			return nil, errors.New("actor identity mismatch")
		}
		return canonical, err
	})
}

type personalActorLookup func(context.Context, Account) (*activitypub.Actor, error)

func resolvePersonalReferences(ctx context.Context, conn *gorm.DB, b *Bundle, discover personalActorLookup) error {
	if b == nil || b.Scope != "personal" {
		return nil
	}
	if conn == nil {
		return errors.New("personal reference resolution requires database")
	}
	failed := map[string]bool{}
	for n := range b.Accounts {
		if err := ctx.Err(); err != nil {
			return err
		}
		a := &b.Accounts[n]
		if a.ID == b.OwnerID {
			continue
		}
		// Never accept a verification flag supplied by a backup.
		if a.Settings == nil {
			a.Settings = map[string]any{}
		}
		delete(a.Settings, "personal_reference_verified")
		var cached models.Account
		query := conn.WithContext(ctx)
		if a.ActorURI != "" {
			query = query.Where("origin_id = ? OR local_actor_uri = ?", a.ActorURI, a.ActorURI)
		} else {
			domain := a.Domain
			base, _ := url.Parse(systemsettings.GetBaseURL())
			if domain == systemsettings.GetDomain() || (base != nil && domain == base.Host) {
				domain = "__LOCAL__"
			}
			query = query.Where("acct = ?", a.Username+"@"+domain)
		}
		err := query.First(&cached).Error
		// A legacy acct-only cache row is not a verified actor URI.
		if err == nil && cached.Domain != nil && cached.OriginId == "" {
			err = gorm.ErrRecordNotFound
		}
		if err == nil {
			a.ActorURI = cached.ActorURI(systemsettings.GetBaseURL())
			a.Username = cached.Username
			a.Local = false
			if cached.Domain != nil {
				a.Domain = *cached.Domain
			} else {
				u, _ := url.Parse(a.ActorURI)
				a.Domain = u.Host
			}
			a.DisplayName = cached.DisplayName
			a.Note = cached.Note
			a.Locked = cached.Locked
			a.Bot = cached.Bot
			a.Settings["personal_reference_verified"] = true
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var actor *activitypub.Actor
		if a.ActorURI != "" || (a.Username != "" && a.Domain != "" && !strings.ContainsAny(a.Domain, "/?#@")) {
			requestCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			actor, err = discover(requestCtx, *a)
			cancel()
		} else {
			err = errors.New("missing portable account identity")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		valid := false
		if err == nil && actor != nil {
			u, parseErr := url.Parse(actor.ID)
			valid = parseErr == nil && u.Host != "" && u.User == nil && (u.Scheme == "https" || u.Scheme == "http") && actor.PreferredUsername != "" && actor.Inbox != ""
			if a.ActorURI != "" && actor.ID != a.ActorURI {
				valid = false
			}
			if valid {
				a.ActorURI = actor.ID
				a.Username = actor.PreferredUsername
				a.Domain = u.Host
				a.Local = false
				a.DisplayName = actor.Name
				a.Note = actor.Summary
				a.NoteHTML = true
				a.Inbox = actor.Inbox
				a.Locked = actor.ManuallyApprovesFollowers
				a.Bot = actor.Type == "Service"
				if actor.Endpoints != nil {
					a.SharedInbox = actor.Endpoints.SharedInbox
				}
				a.Settings["personal_reference_verified"] = true
			}
		}
		if !valid {
			failed[a.ID] = true
			// A supplied but unverifiable URI is not permission to create a remote actor.
			a.ActorURI = ""
			parserIssue(b, "warning", "unresolved_personal_reference", "account", a.ID, "The referenced account could not be verified; dependent data remains a private archive.")
		}
	}
	for n := range b.Posts {
		p := &b.Posts[n]
		if failed[p.AuthorID] {
			p.ArchiveOnly = true
		}
		for _, id := range p.RecipientIDs {
			if failed[id] {
				p.ArchiveOnly = true
			}
		}
	}
	return nil
}
