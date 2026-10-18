package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/kasuganosora/community/cache"
	"github.com/kasuganosora/community/db"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/defaultavatar"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/systemsettings"
	"time"
)

func buildActorProfile(account *models.Account, tags []Tag) (Actor, error) {
	baseURL := systemsettings.GetBaseURL()
	username := account.Username
	actorURL := account.ActorURI(baseURL)

	// Build actor profile
	actor := Actor{
		Context: []string{
			"https://www.w3.org/ns/activitystreams",
			"https://w3id.org/security/v1",
		},
		ID:                        actorURL,
		Aliases:                   account.AlsoKnownAs,
		MovedTo:                   account.MovedTo,
		Type:                      "Person", //nolint:goconst
		Following:                 actorURL + "/following",
		Followers:                 actorURL + "/followers",
		Inbox:                     actorURL + "/inbox",
		Outbox:                    actorURL + "/outbox",
		PreferredUsername:         account.Username,
		ManuallyApprovesFollowers: account.Locked,
		Discoverable:              discoveryBool(account.Discoverable),
		Indexable:                 discoveryBool(!account.NoIndex),
		HideCollections:           discoveryBool(account.HideCollections),
		IsCat:                     discoveryBool(account.IsCat),
		Name:                      account.DisplayName,
		Summary:                   account.NoteHTML(htmlsafe.OptionsForBaseURL(baseURL)),
		URL:                       baseURL + "/@" + username,
		Endpoints: &Endpoints{
			SharedInbox: baseURL + "/inbox",
		},
	}

	profileEmojiTags := tags
	if len(profileEmojiTags) > 0 {
		actor.Tag = profileEmojiTags
	}

	contextValues := []any{"https://www.w3.org/ns/activitystreams", "https://w3id.org/security/v1"}
	if len(profileEmojiTags) > 0 {
		contextValues = emojiContext()
	}
	actor.Context = append(contextValues, map[string]any{
		"toot":            "http://joinmastodon.org/ns#",
		"discoverable":    "toot:discoverable",
		"indexable":       "toot:indexable",
		"hideCollections": "toot:hideCollections",
		"misskey":         "https://misskey-hub.net/ns#",
		"isCat":           "misskey:isCat",
	})

	avatar := account.Avatar
	if defaultavatar.IsPlaceholder(avatar, baseURL) {
		avatar = baseURL + defaultavatar.Path(defaultavatar.Seed(account.Username, systemsettings.GetDomain()))
	}
	if avatar != "" {
		actor.Icon = &Image{
			Type: "Image", //nolint:goconst
			URL:  avatar,
		}
	}

	if account.Header != "" {
		actor.Image = &Image{
			Type: "Image",
			URL:  account.Header,
		}
	}

	accountKey, err := localAccountKey(account)
	if err != nil {
		return Actor{}, err
	}
	actor.PublicKey = &PublicKey{
		ID: accountKey.SigningKeyID(actorURL), Owner: actorURL, PublicKeyPem: accountKey.PublicKey,
	}

	if account.LocalActorURI != "" {
		var keys []models.AccountFederationKey
		if err := db.GetDB().Where("account_id = ? AND revoked = ? AND (expires_at IS NULL OR expires_at > ?)", account.Id, false, time.Now()).Order("id ASC").Find(&keys).Error; err != nil {
			return Actor{}, err
		}
		for _, key := range keys {
			if supportedRSAAlgorithm(key.Algorithm) && key.KeyID != actor.PublicKey.ID {
				actor.PublicKeys = append(actor.PublicKeys, &PublicKey{ID: key.KeyID, Owner: actorURL, PublicKeyPem: key.PublicKey})
			}
		}
	}
	// Bot/Service accounts use "Service" actor type
	if account.Bot {
		actor.Type = "Service" //nolint:goconst
	}
	// The instance actor (<base>/actor) is the server itself, as on Mastodon.
	if isInstanceActor(account) {
		actor.Type = "Application"
		actor.URL = baseURL + "/about"
		actor.ManuallyApprovesFollowers = true
	}

	// Add profile fields
	if account.Extra != nil {
		for _, field := range serializer.ToActivityPubFields(account.Extra.Fields, account.IsLocal()) {
			actor.Attachments = append(actor.Attachments, Attachment{
				Type:  "PropertyValue",
				Name:  field.Name,
				Value: field.Value,
			})
		}
	}

	return actor, nil
}

func profileUpdatePayload(e *models.FederationOutbox, actorURI, activityID string) (map[string]any, error) {
	var snapshot models.FederationProfileSnapshot
	if err := json.Unmarshal([]byte(e.Payload), &snapshot); err != nil {
		return nil, err
	}
	account := &snapshot.Account
	if account.Id != e.AccountID || account.Domain != nil {
		return nil, errors.New("profile snapshot must belong to local author")
	}
	account.Extra = snapshot.Extra
	account.Emojis = snapshot.Emojis
	actor, err := buildActorProfile(account, emojiTags(snapshot.Emojis, models.AccountEmojiText(account), systemsettings.GetBaseURL()))
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(actor)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(encoded, &object); err != nil {
		return nil, err
	}
	// Update semantics distinguish omission from clearing. Actor pull responses
	// may omit empty optional fields, but updates must explicitly clear them.
	object["name"] = actor.Name
	object["summary"] = actor.Summary
	if actor.Name == "" {
		object["name"] = nil
	}
	if actor.Summary == "" {
		object["summary"] = nil
	}
	if actor.Tag == nil {
		object["tag"] = []Tag{}
	}
	return map[string]any{"id": activityID, "type": "Update", "actor": actorURI, "to": []string{actorURI + "/followers"}, "object": object}, nil
}

func profileRecipientAllowed(author, target *models.Account) (bool, error) {
	allowed, err := outboundRecipientAllowed(nil, author, target)
	if err != nil || !allowed {
		return allowed, err
	}
	if target.Domain == nil {
		return false, nil
	}
	var count int64
	err = db.GetDB().Model(&models.Follow{}).Where("account_id = ? AND follow_id = ? AND status = ?", target.Id, author.Id, models.FollowStatusFollowed).Count(&count).Error
	return count > 0, err
}

type profileInvalidationsKey struct{}

func beginProfileInvalidations(ctx context.Context) (context.Context, func()) {
	accounts := make([]models.Account, 0)
	return context.WithValue(ctx, profileInvalidationsKey{}, &accounts), func() {
		for _, a := range accounts {
			invalidateProfile(ctx, &a)
		}
	}
}
func queueProfileInvalidation(ctx context.Context, account *models.Account) {
	if pending, ok := ctx.Value(profileInvalidationsKey{}).(*[]models.Account); ok {
		*pending = append(*pending, *account)
		return
	}
	invalidateProfile(ctx, account)
}
func invalidateProfile(ctx context.Context, account *models.Account) {
	getDefaultResolver().accountCache.Delete(account.OriginId)
	if len(cRedis.GetRedises()) > 0 {
		_ = cache.InvalidateAccount(ctx, account.Id)
	}
}

// Actor updates may omit a field (preserve), explicitly clear it (null), or
// supply the same image/link shapes accepted during actor discovery. No DNS or
// image download happens here; subsequent fetches retain the pinned transport.
func actorImageUpdates(object map[string]any, updates map[string]any) error {
	for field, columns := range map[string][]string{"icon": {"avatar", "avatar_static"}, "image": {"header", "header_static"}} {
		value, present := object[field]
		if !present {
			continue
		}
		imageURL := ""
		if value != nil {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			image := firstImage(raw)
			if image == nil || !safeEmojiURL(image.URL) || remoteMediaURL(image.URL) != nil {
				return errors.New("invalid actor image")
			}
			imageURL = image.URL
		}
		for _, column := range columns {
			updates[column] = imageURL
		}
	}
	return nil
}
