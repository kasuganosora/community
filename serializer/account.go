package serializer

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/defaultassets"
	"github.com/kasuganosora/community/defaultavatar"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// AvatarDecoration is one decoration in Misskey's geometry (see
// models.AvatarDecorationSelection). URL is always same-site: the local
// image endpoint or a media proxy link.
type AvatarDecoration struct {
	ID      string  `json:"id"`
	URL     string  `json:"url"`
	Angle   float64 `json:"angle,omitempty"`
	FlipH   bool    `json:"flip_h,omitempty"`
	OffsetX float64 `json:"offset_x,omitempty"`
	OffsetY float64 `json:"offset_y,omitempty"`
}

type Account struct {
	Moved               *Account           `json:"moved,omitempty"`
	AvatarDecorations   []AvatarDecoration `json:"avatar_decorations,omitempty"`
	AvatarDecorationURL string             `json:"avatar_decoration_url,omitempty"`
	AvatarDecorationID  string             `json:"avatar_decoration_id,omitempty"`
	Id                  string             `json:"id"`
	Username            string             `json:"username"`
	Acct                string             `json:"acct"`
	DisplayName         string             `json:"display_name"`
	Locked              bool               `json:"locked"`
	Bot                 bool               `json:"bot"`
	IsCat               bool               `json:"is_cat"`
	Location            string             `json:"location"`
	Birthday            string             `json:"birthday"`
	Language            string             `json:"language"`
	ReactionAcceptance  string             `json:"reaction_acceptance"`
	FollowedMessage     string             `json:"followed_message,omitempty"`
	Discoverable        bool               `json:"discoverable"`
	Indexable           bool               `json:"indexable"`
	Group               bool               `json:"group"`
	CreatedAt           *APITime           `json:"created_at"`
	Note                string             `json:"note"`
	Url                 string             `json:"url"`
	Uri                 string             `json:"uri"`
	Avatar              string             `json:"avatar"`
	AvatarDefault       string             `json:"avatar_default"`
	AvatarStatic        string             `json:"avatar_static"`
	AvatarDescription   string             `json:"avatar_description,omitempty"`
	Header              string             `json:"header"`
	HeaderStatic        string             `json:"header_static"`
	HeaderDescription   string             `json:"header_description,omitempty"`
	FollowersCount      int                `json:"followers_count"`
	FollowingCount      int                `json:"following_count"`
	StatusesCount       int                `json:"statuses_count"`
	LastStatusAt        *APIDate           `json:"last_status_at"`
	HideCollections     bool               `json:"hide_collections"`
	Emoji               []*Emoji           `json:"emojis"`
	Fields              []Field            `json:"fields"`
	NoIndex             bool               `json:"noindex,omitempty"`
	// Roles is always an array in Mastodon >= 4.3 (empty for plain users).
	Roles []any `json:"roles"`

	intId uint64 `json:"-"`
}

// accountAddress returns the Mastodon acct, profile URL and ActivityPub actor
// URI of an account. Like Mastodon, local accounts use the bare username as
// acct and remote accounts use username@domain. Local URLs are built from the
// instance base URL (scheme included).
func accountAddress(account *models.Account) (acct, profileURL, actorURI string) {
	if account.Domain != nil && *account.Domain != "" {
		domain := *account.Domain
		actorURI = "https://" + domain + "/users/" + account.Username
		// Remote implementations need not identify actors by username (Misskey
		// uses an opaque ID). Preserve the actor URI learned through discovery.
		if origin, err := url.Parse(account.OriginId); err == nil && origin.Host != "" &&
			(origin.Scheme == "https" || origin.Scheme == "http") && origin.User == nil {
			actorURI = account.OriginId
		}

		return account.Username + "@" + domain,
			"https://" + domain + "/@" + account.Username,
			actorURI
	}

	base := systemsettings.GetBaseURL()

	return account.Username, base + "/@" + account.Username, account.ActorURI(base)
}

func ToAccount(account *models.Account) (acc *Account) {
	if account == nil {
		return nil
	}

	if account.DeletionRequestedAt != nil {
		return toAccount(account, contentRenderOptions(), nil)
	}
	return toAccount(account, contentRenderOptions(), accountEmojis(account))
}
func toAccount(account *models.Account, options htmlsafe.RenderOptions, emojis []*Emoji) (acc *Account) {
	if account == nil {
		return nil
	}

	// A deletion request hides profile data immediately, including references
	// embedded in old notifications while the background worker is running.
	if account.DeletionRequestedAt != nil {
		account = &models.Account{Id: account.Id, Username: account.Username, Domain: account.Domain,
			OriginId: account.OriginId, LocalActorURI: account.LocalActorURI, CreatedAt: account.CreatedAt,
			DeletionRequestedAt: account.DeletionRequestedAt, IsBan: true, NoIndex: true, HideCollections: true}
		emojis = nil
	}
	acct, profileURL, actorURI := accountAddress(account)
	domain := systemsettings.GetDomain()
	if account.Domain != nil && *account.Domain != "" {
		domain = *account.Domain
	}
	fallback := strings.TrimRight(systemsettings.GetBaseURL(), "/") + defaultavatar.Path(defaultavatar.Seed(account.Username, domain))
	avatar := proxiedAccountMedia(account, mediaproxy.KindAvatar, account.Avatar)
	if defaultavatar.IsPlaceholder(account.Avatar, systemsettings.GetBaseURL()) {
		avatar = fallback
	}
	static := proxiedAccountMedia(account, mediaproxy.KindAvatar, account.AvatarStatic)
	if defaultavatar.IsPlaceholder(account.AvatarStatic, systemsettings.GetBaseURL()) {
		static = avatar
	}

	acc = &Account{
		Username:            account.Username,
		AvatarDecorations:   AvatarDecorations(account),
		AvatarDecorationURL: avatarDecorationURL(account),
		AvatarDecorationID:  strconv.FormatUint(legacyAvatarDecorationID(account), 10),
		Acct:                acct,
		DisplayName:         account.DisplayName,
		Locked:              account.Locked,
		Bot:                 account.Bot,
		IsCat:               account.IsCat,
		Location:            account.Location,
		Birthday:            account.Birthday,
		Language:            account.Language,
		ReactionAcceptance:  account.ReactionAcceptance,
		Discoverable:        account.Discoverable,
		Indexable:           !account.NoIndex,
		Group:               account.Group,
		CreatedAt:           newAPIDayTime(account.CreatedAt),
		Note:                account.NoteHTML(options),
		Url:                 profileURL,
		Uri:                 actorURI,
		Avatar:              avatar,
		AvatarDefault:       fallback,
		AvatarStatic:        static,
		Header:              mediaOrDefault(proxiedAccountMedia(account, mediaproxy.KindHeader, account.Header), "header.png"),
		HeaderStatic:        mediaOrDefault(proxiedAccountMedia(account, mediaproxy.KindHeader, account.HeaderStatic), "header.png"),
		LastStatusAt:        newAPIDate(account.LastStatusAt),
		Roles:               []any{},
		HideCollections:     account.HideCollections,
		NoIndex:             account.NoIndex,
		Emoji:               emojis,
		intId:               account.Id,
	}

	acc.Id = strconv.FormatUint(account.Id, 10)

	if legacyAvatarDecorationID(account) == 0 {
		acc.AvatarDecorationID = ""
	}

	if acc.Fields == nil {
		acc.Fields = []Field{}
	}

	if acc.Emoji == nil {
		acc.Emoji = []*Emoji{}
	}

	if account.Extra != nil {
		acc.FollowersCount = account.Extra.FollowersCount
		acc.FollowingCount = account.Extra.FollowingCount
		acc.StatusesCount = account.Extra.StatusesCount

		acc.Fields = toAPIFields(account.Extra.Fields, account.IsLocal(), options)
	}

	if account.MovedTo != "" && db.GetDB() != nil {
		var target models.Account
		parsed, parseErr := url.Parse(account.MovedTo)
		if parseErr == nil {
			err := db.GetDB().Preload("Extra").Where("origin_id = ? OR local_actor_uri = ?", account.MovedTo, account.MovedTo).First(&target).Error
			if err != nil {
				base, _ := url.Parse(systemsettings.GetBaseURL())
				if base != nil && parsed.Scheme == base.Scheme && parsed.Host == base.Host {
					err = db.GetDB().Preload("Extra").Where("domain IS NULL AND username = ?", path.Base(parsed.Path)).First(&target).Error
				}
			}
			if err == nil && target.Id != account.Id && target.ActorURI(systemsettings.GetBaseURL()) == account.MovedTo {
				target.MovedTo = "" // Bound recursion even if a peer publishes a cycle.
				acc.Moved = ToAccount(&target)
			}
		}
	}

	return acc
}

func ToAccounts(accounts []*models.Account) (accs []*Account) {
	accs = make([]*Account, len(accounts))
	ctx, err := newAccountSerialization(accounts)
	for i, a := range accounts {
		if err != nil {
			accs[i] = ToAccount(a)
		} else {
			accs[i] = ctx.account(a)
		}
	}
	return accs
}

type Mention struct {
	Id       string `json:"id"`
	Username string `json:"username"`
	Url      string `json:"url"`
	Acct     string `json:"acct"`
}

func ToMention(account *models.Account) (mention *Mention) {
	acct, profileURL, _ := accountAddress(account)

	mention = &Mention{
		Username: account.Username,
		Acct:     acct,
		Url:      profileURL,
	}

	mention.Id = strconv.FormatUint(account.Id, 10)

	return
}

func ToMentions(accounts []*models.Account) (mentions []*Mention) {
	mentions = make([]*Mention, len(accounts))
	for i, account := range accounts {
		mentions[i] = ToMention(account)
	}

	return
}

func accountEmojis(account *models.Account) []*Emoji {
	domain := ""
	if account.Domain != nil {
		domain = *account.Domain
	}
	emojis := account.Emojis
	text := models.AccountEmojiText(account)
	if domain == "" && len(models.MatchEmojiTags(models.EmojiText(text))) > 0 && db.GetDB() != nil {
		if found, err := models.GetEmojiFromText(db.GetDB(), text); err == nil {
			for _, e := range found {
				emojis = append(emojis, e)
			}
		}
	}
	if domain != "" && emojis == nil && account.Id != 0 && len(models.MatchEmojiTags(models.EmojiText(text))) > 0 && db.GetDB() != nil {
		_ = db.GetDB().Model(account).Association("Emojis").Find(&emojis)
	}
	return ToEmojis(models.EmojisInText(emojis, text, domain, false))
}

// legacyAvatarDecorationID is the first local decoration, kept in the old
// single-decoration fields for clients that predate avatar_decorations.
func legacyAvatarDecorationID(account *models.Account) uint64 {
	if !account.IsLocal() {
		return 0
	}
	for _, d := range account.EffectiveAvatarDecorations() {
		if id := d.LocalID(); id != 0 {
			return id
		}
	}
	return 0
}

func avatarDecorationURL(account *models.Account) string {
	id := legacyAvatarDecorationID(account)
	if id == 0 {
		return ""
	}
	return localAvatarDecorationURL(id)
}

func localAvatarDecorationURL(id uint64) string {
	return "/api/v1/avatar_decorations/" + strconv.FormatUint(id, 10) + "/image"
}

// AvatarDecorations serializes the account's decorations. Local entries point
// at this server's image endpoint; remote entries are only exposed when the
// media proxy can serve them, so clients never hotlink a third-party host.
func AvatarDecorations(account *models.Account) []AvatarDecoration {
	list := account.EffectiveAvatarDecorations()
	if len(list) == 0 {
		return nil
	}
	local := account.IsLocal()
	proxyPrefix := strings.TrimRight(systemsettings.GetBaseURL(), "/") + mediaproxy.PathPrefix
	out := make([]AvatarDecoration, 0, len(list))
	for _, d := range list {
		d = d.Normalized()
		var u string
		if local {
			id := d.LocalID()
			if id == 0 {
				continue
			}
			u = localAvatarDecorationURL(id)
		} else {
			if d.URL == "" {
				continue
			}
			u = mediaproxy.URL(mediaproxy.KindEmoji, d.URL)
			if !strings.HasPrefix(u, proxyPrefix) {
				continue
			}
		}
		out = append(out, AvatarDecoration{ID: d.ID, URL: u, Angle: d.Angle, FlipH: d.FlipH, OffsetX: d.OffsetX, OffsetY: d.OffsetY})
		if len(out) == models.MaxAvatarDecorations {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// proxiedAccountMedia routes a remote account's avatar or header through the
// media proxy; local accounts' files are served by this server already.
func proxiedAccountMedia(account *models.Account, kind, raw string) string {
	if account.IsLocal() {
		return raw
	}

	return mediaproxy.URL(kind, raw)
}

// mediaOrDefault never lets an avatar/header URL be empty: Mastodon clients
// decode these as non-optional URLs, so fall back to the bundled PNG.
func mediaOrDefault(raw, asset string) string {
	if raw = absoluteURL(raw); isAbsoluteURL(raw) {
		return raw
	}

	return DefaultAssetURL(asset)
}

// DefaultAssetURL returns the absolute URL of a bundled default asset.
func DefaultAssetURL(name string) string {
	return strings.TrimRight(systemsettings.GetBaseURL(), "/") + defaultassets.Path(name)
}
