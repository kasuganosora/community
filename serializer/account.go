package serializer

import (
	"fmt"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"strconv"
	"time"
)

type Account struct {
	Id             string     `json:"id"`
	Username       string     `json:"username"`
	Acct           string     `json:"acct"`
	DisplayName    string     `json:"display_name"`
	Locked         bool       `json:"locked"`
	Bot            bool       `json:"bot"`
	Discoverable   bool       `json:"discoverable"`
	Group          bool       `json:"group"`
	CreatedAt      *time.Time `json:"created_at"`
	Note           string     `json:"note"`
	Url            string     `json:"url"`
	Avatar         string     `json:"avatar"`
	AvatarStatic   string     `json:"avatar_static"`
	Header         string     `json:"header"`
	HeaderStatic   string     `json:"header_static"`
	FollowersCount int        `json:"followers_count"`
	FollowingCount int        `json:"following_count"`
	StatusesCount  int        `json:"statuses_count"`
	LastStatusAt   *time.Time `json:"last_status_at"`
	Emoji          []*Emoji   `json:"emojis"`
	Fields         []Field    `json:"fields"`
}

func ToAccount(account *models.Account) (acc *Account) {
	var domain string
	if domain == "" {
		domain = systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	}
	acc = &Account{
		Username:     account.Username,
		Acct:         account.Username + "@" + domain,
		DisplayName:  account.DisplayName,
		Locked:       account.Locked,
		Bot:          account.Bot,
		Discoverable: account.Discoverable,
		Group:        account.Group,
		CreatedAt:    account.CreatedAt,
		Note:         account.Note,
		Url:          account.Website,
		Avatar:       account.Avatar,
		AvatarStatic: account.AvatarStatic,
		Header:       account.Header,
		HeaderStatic: account.HeaderStatic,
		LastStatusAt: account.LastStatusAt,
		Emoji:        ToEmojis(account.Emojis),
	}

	acc.Id = strconv.FormatUint(account.Id, 10)

	if account.Extra != nil {
		acc.FollowersCount = account.Extra.FollowersCount
		acc.FollowingCount = account.Extra.FollowingCount
		acc.StatusesCount = account.Extra.StatusesCount

		acc.Fields = ToFields(account.Extra.Fields)
	}

	return
}

func ToAccounts(accounts []*models.Account) (accs []*Account) {
	accs = make([]*Account, len(accounts))
	for i, account := range accounts {
		accs[i] = ToAccount(account)
	}

	return
}

type Mention struct {
	Id       string `json:"id"`
	Username string `json:"username"`
	Url      string `json:"url"`
	Acct     string `json:"acct"`
}

func ToMention(account *models.Account) (mention *Mention) {
	var domain string
	if domain == "" {
		domain = systemsettings.GetString(systemsettings.KeyDomain, "localhost")
	}
	mention = &Mention{
		Username: account.Username,
		Acct:     account.Username + "@" + domain,
		Url:      fmt.Sprintf("https://%s/users/%s", domain, account.Username),
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
