package serializer

import (
	"github.com/kasuganosora/community/models"
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
}

func ToAccount(account *models.Account, extra models.AccountExtra) *Account {
	acc := &Account{
		Id:             account.OriginId,
		Username:       account.Username,
		Acct:           account.Username + "@" + account.Domain,
		DisplayName:    account.DisplayName,
		Locked:         account.Locked,
		Bot:            account.Bot,
		Discoverable:   account.Discoverable,
		Group:          account.Group,
		CreatedAt:      account.CreatedAt,
		Note:           account.Note,
		Url:            account.Website,
		Avatar:         account.Avatar,
		AvatarStatic:   account.AvatarStatic,
		Header:         account.Header,
		HeaderStatic:   account.HeaderStatic,
		FollowersCount: extra.FollowersCount,
		FollowingCount: extra.FollowingCount,
		StatusesCount:  extra.StatusesCount,
		LastStatusAt:   account.LastStatusAt,
		Emoji:          ToEmojis(account.Emojis),
	}

	return acc
}
