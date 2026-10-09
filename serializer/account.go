package serializer

import (
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/defaultassets"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/mediaproxy"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"net/url"
	"strconv"
	"strings"
)

type Account struct {
	AvatarDecorationURL string   `json:"avatar_decoration_url,omitempty"`
	AvatarDecorationID  string   `json:"avatar_decoration_id,omitempty"`
	Id                  string   `json:"id"`
	Username            string   `json:"username"`
	Acct                string   `json:"acct"`
	DisplayName         string   `json:"display_name"`
	Locked              bool     `json:"locked"`
	Bot                 bool     `json:"bot"`
	Discoverable        bool     `json:"discoverable"`
	Indexable           bool     `json:"indexable"`
	Group               bool     `json:"group"`
	CreatedAt           *APITime `json:"created_at"`
	Note                string   `json:"note"`
	Url                 string   `json:"url"`
	Uri                 string   `json:"uri"`
	Avatar              string   `json:"avatar"`
	AvatarStatic        string   `json:"avatar_static"`
	AvatarDescription   string   `json:"avatar_description"`
	Header              string   `json:"header"`
	HeaderStatic        string   `json:"header_static"`
	HeaderDescription   string   `json:"header_description"`
	FollowersCount      int      `json:"followers_count"`
	FollowingCount      int      `json:"following_count"`
	StatusesCount       int      `json:"statuses_count"`
	LastStatusAt        *APIDate `json:"last_status_at"`
	HideCollections     bool     `json:"hide_collections"`
	Emoji               []*Emoji `json:"emojis"`
	Fields              []Field  `json:"fields"`
	NoIndex             bool     `json:"noindex,omitempty"`
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

	return toAccount(account, contentRenderOptions(), accountEmojis(account))
}
func toAccount(account *models.Account, options htmlsafe.RenderOptions, emojis []*Emoji) (acc *Account) {
	if account == nil {
		return nil
	}

	acct, profileURL, actorURI := accountAddress(account)

	acc = &Account{
		Username:            account.Username,
		AvatarDecorationURL: avatarDecorationURL(account),
		AvatarDecorationID:  strconv.FormatUint(account.AvatarDecorationID, 10),
		Acct:                acct,
		DisplayName:         account.DisplayName,
		Locked:              account.Locked,
		Bot:                 account.Bot,
		Discoverable:        account.Discoverable,
		Indexable:           !account.NoIndex,
		Group:               account.Group,
		CreatedAt:           newAPIDayTime(account.CreatedAt),
		Note:                account.NoteHTML(options),
		Url:                 profileURL,
		Uri:                 actorURI,
		Avatar:              mediaOrDefault(proxiedAccountMedia(account, mediaproxy.KindAvatar, account.Avatar), "avatar.png"),
		AvatarStatic:        mediaOrDefault(proxiedAccountMedia(account, mediaproxy.KindAvatar, account.AvatarStatic), "avatar.png"),
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

	if account.AvatarDecorationID == 0 {
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

func avatarDecorationURL(account *models.Account) string {
	if account.AvatarDecorationID == 0 || !account.IsLocal() {
		return ""
	}
	return "/api/v1/avatar_decorations/" + strconv.FormatUint(account.AvatarDecorationID, 10) + "/image"
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
	if raw != "" {
		return raw
	}

	return DefaultAssetURL(asset)
}

// DefaultAssetURL returns the absolute URL of a bundled default asset.
func DefaultAssetURL(name string) string {
	return strings.TrimRight(systemsettings.GetBaseURL(), "/") + defaultassets.Path(name)
}
