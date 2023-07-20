package serializer

import (
	"fmt"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"strconv"
	"time"
)

/**
{
    "id": "110739683093477367",
    "created_at": "2023-07-19T07:57:45.178Z",
    "in_reply_to_id": null,
    "in_reply_to_account_id": null,
    "sensitive": false,
    "spoiler_text": "",
    "visibility": "public",
    "language": "zh",
    "uri": "https://meow.cloud/users/luna/statuses/110739683093477367",
    "url": "https://meow.cloud/@luna/110739683093477367",
    "replies_count": 0,
    "reblogs_count": 0,
    "favourites_count": 0,
    "edited_at": null,
    "favourited": false,
    "reblogged": false,
    "muted": false,
    "bookmarked": false,
    "pinned": false,
    "content": "\u003cp\u003e啥时候能下班（\u003cbr /\u003e🐈\u003c/p\u003e",
    "filtered": [],
    "reblog": null,
    "application": {
        "name": "Web",
        "website": null
    },
    "account": {
        "id": "109764173071930539",
        "username": "luna",
        "acct": "luna",
        "display_name": "Luna",
        "locked": false,
        "bot": false,
        "discoverable": true,
        "group": false,
        "created_at": "2023-01-28T00:00:00.000Z",
        "note": "\u003cp\u003e@luna@hime.at\u003c/p\u003e",
        "url": "https://meow.cloud/@luna",
        "avatar": "https://files.meow.cloud/accounts/avatars/109/764/173/071/930/539/original/96013feefb91d6a9.png",
        "avatar_static": "https://files.meow.cloud/accounts/avatars/109/764/173/071/930/539/original/96013feefb91d6a9.png",
        "header": "https://files.meow.cloud/accounts/headers/109/764/173/071/930/539/original/a70552bde4ca911d.jpg",
        "header_static": "https://files.meow.cloud/accounts/headers/109/764/173/071/930/539/original/a70552bde4ca911d.jpg",
        "followers_count": 51,
        "following_count": 108,
        "statuses_count": 961,
        "last_status_at": "2023-07-19",
        "noindex": false,
        "emojis": [],
        "roles": [],
        "fields": []
    },
    "media_attachments": [],
    "mentions": [],
    "tags": [],
    "emojis": [],
    "card": null,
    "poll": null
}
*/

type Status struct {
	Id                 string        `json:"id"`
	CreatedAt          *time.Time    `json:"created_at"`
	InReplyToId        *string       `json:"in_reply_to_id"`
	InReplyToAccountId *string       `json:"in_reply_to_account_id"`
	Sensitive          bool          `json:"sensitive"`
	SpoilerText        string        `json:"spoiler_text"`
	Visibility         string        `json:"visibility"`
	Language           string        `json:"language"`
	Uri                string        `json:"uri"`
	Url                string        `json:"url"`
	RepliesCount       uint64        `json:"replies_count"`
	ReblogsCount       uint64        `json:"reblogs_count"`
	FavouritesCount    uint64        `json:"favourites_count"`
	EditedAt           *time.Time    `json:"edited_at"`
	Favourited         bool          `json:"favourited"`
	Reblogged          bool          `json:"reblogged"`
	Muted              bool          `json:"muted"`
	Bookmarked         bool          `json:"bookmarked"`
	Pinned             bool          `json:"pinned"`
	Content            string        `json:"content"`
	Filtered           []string      `json:"filtered"`
	Reblog             *Status       `json:"reblog"`
	Application        *Application  `json:"application"`
	Account            *Account      `json:"account"`
	MediaAttachments   []*Attachment `json:"media_attachments"`
	Mentions           []*Mention    `json:"mentions"`
	Tags               []*Tag        `json:"tags"`
	Emojis             []*Emoji      `json:"emojis"`
	Card               Card          `json:"card"`
	Poll               *Poll         `json:"poll"`
}

func ToStatusBase(status *models.Status) (ret *Status, err error) {
	if status == nil {
		return
	}
	ret = &Status{}
	ret.Id = strconv.FormatUint(status.Id, 10)
	ret.CreatedAt = status.CreatedAt
	if status.InReplyToId > 0 {
		ret.InReplyToId = new(string)
		*ret.InReplyToId = strconv.FormatUint(status.InReplyToId, 10)
	}
	if status.InReplyToAccountId > 0 {
		ret.InReplyToAccountId = new(string)
		*ret.InReplyToAccountId = strconv.FormatUint(status.InReplyToAccountId, 10)
	}

	ret.Sensitive = status.Sensitive
	ret.SpoilerText = status.Content.SpoilerText
	ret.Visibility = status.GetVisibilityString()
	ret.Language = status.Language
	if status.OriginUri != nil {
		ret.Uri = *status.OriginUri
	} else {
		ret.Uri = fmt.Sprintf("%s/%s/statuses/%d", systemsettings.GetString(systemsettings.KeyDomain, ""), status.Account.Username, status.Id)
	}

	if status.OriginUri != nil {
		ret.Url = *status.OriginUri
	} else {
		ret.Url = fmt.Sprintf("%s/@%s/%d", systemsettings.GetString(systemsettings.KeyDomain, ""), status.Account.Username, status.Id)
	}

	ret.RepliesCount = status.RepliesCount
	ret.ReblogsCount = status.ReblogsCount
	ret.FavouritesCount = status.FavouritesCount

	if status.UpdatedAt != nil || status.UpdatedAt.Unix() > status.CreatedAt.Unix() {
		ret.EditedAt = status.UpdatedAt
	}

	ret.Content = status.Content.Content

	ret.Reblog, err = ToStatusBase(status.Relog)
	if err != nil {
		return
	}

	ret.Application = ToApplication(status.Application)
	ret.Mentions = ToMentions(status.ToAccounts)
	ret.Tags = ToTags(status.Tags)
	ret.Emojis = ToEmojis(status.Emojis)
	ret.Card = status.Content.Card

	return
}
