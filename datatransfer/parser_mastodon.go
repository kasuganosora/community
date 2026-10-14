package datatransfer

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

func parseMastodon(ctx context.Context, a *Archive) (*Bundle, error) {
	var actor map[string]any
	if err := readJSON(a, "actor.json", &actor); err != nil {
		return nil, err
	}
	owner := text(actor["id"])
	if owner == "" {
		return nil, errors.New("Mastodon actor ID is missing")
	}
	b := &Bundle{Schema: Schema, Scope: "personal", OwnerID: owner, Source: Source{Platform: "mastodon", BaseURL: origin(owner)}}
	profile := Account{ID: owner, ActorURI: owner, Local: true, Username: text(actor["preferredUsername"]), DisplayName: text(actor["name"]), Note: text(actor["summary"]), NoteHTML: true, URL: text(actor["url"]), Locked: boolean(actor["manuallyApprovesFollowers"]), Bot: text(actor["type"]) == "Service", Discoverable: boolean(actor["discoverable"]), NoIndex: !boolean(actor["indexable"]), HideCollections: boolean(actor["hideCollections"]), CreatedAt: timestamp(actor["published"])}
	for _, field := range arr(actor["attachment"]) {
		f := obj(field)
		if text(f["type"]) == "PropertyValue" {
			profile.Fields = append(profile.Fields, Field{Name: text(f["name"]), Value: text(f["value"])})
		}
	}
	for _, key := range []string{"icon", "image"} {
		im := obj(actor[key])
		uri := text(im["url"])
		if uri == "" {
			continue
		}
		m := Media{ID: "profile:" + key, OwnerID: owner, Name: key, ContentType: text(im["mediaType"])}
		if _, ok := a.Files[uri]; ok {
			m.Path = uri
		} else {
			m.RemoteURL = uri
		}
		b.Media = append(b.Media, m)
		if key == "icon" {
			profile.AvatarID = m.ID
			profile.AvatarURL = m.RemoteURL
		} else {
			profile.HeaderID = m.ID
			profile.HeaderURL = m.RemoteURL
		}
	}
	b.Accounts = append(b.Accounts, profile)
	outbox := text(actor["outbox"])
	if outbox == "" || strings.HasPrefix(outbox, "http") {
		outbox = "outbox.json"
	}
	if _, ok := a.Files[outbox]; ok {
		var collection map[string]any
		if err := readJSON(a, outbox, &collection); err != nil {
			return nil, err
		}
		for i, item := range arr(collection["orderedItems"]) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if i >= a.limits.MaxRecords {
				return nil, errors.New("post record limit exceeded")
			}
			activity := obj(item)
			typ := text(activity["type"])
			if typ == "Announce" {
				b.Posts = append(b.Posts, Post{ID: text(activity["id"]), AuthorID: owner, URI: text(activity["id"]), ReblogID: text(activity["object"]), Visibility: "private", ArchiveOnly: true, CreatedAt: timestamp(activity["published"]), Extensions: raw(activity)})
				parserIssue(b, "warning", "archive_only", "post", text(activity["id"]), "A historical boost is preserved without broadcasting it again.")
				continue
			}
			note := obj(activity["object"])
			if typ == "Note" || typ == "Question" {
				note = activity
			}
			if note == nil {
				b.Records = append(b.Records, PreservedRecord{Kind: "activity", ID: fmt.Sprint(i), OwnerID: owner, Data: raw(activity)})
				continue
			}
			author := text(note["attributedTo"])
			if author == "" {
				author = text(activity["actor"])
			}
			if author == "" {
				author = owner
			}
			accountRef(b, author)
			p := Post{Extensions: raw(note), ID: text(note["id"]), AuthorID: author, URI: text(note["id"]), URL: text(note["url"]), HTML: text(note["content"]), SpoilerText: text(note["summary"]), Sensitive: boolean(note["sensitive"]), ReplyID: text(note["inReplyTo"]), ConversationID: text(note["conversation"]), CreatedAt: timestamp(note["published"]), UpdatedAt: timestamp(note["updated"]), ArchiveOnly: author != owner}
			if p.ID == "" {
				return nil, errors.New("Mastodon note ID is missing")
			}
			to, cc := stringsOf(note["to"]), stringsOf(note["cc"])
			p.Visibility = "direct"
			contains := func(items []string, value string) bool {
				for _, x := range items {
					if x == value {
						return true
					}
				}
				return false
			}
			public := "https://www.w3.org/ns/activitystreams#Public"
			if contains(to, public) {
				p.Visibility = "public"
			} else if contains(cc, public) {
				p.Visibility = "unlisted"
			} else if contains(to, owner+"/followers") || contains(to, text(actor["followers"])) {
				p.Visibility = "private"
			}
			for _, recipient := range append(to, cc...) {
				if recipient == public || recipient == text(actor["followers"]) || recipient == owner+"/followers" {
					continue
				}
				if strings.HasPrefix(recipient, "https://") || strings.HasPrefix(recipient, "http://") {
					accountRef(b, recipient)
					p.RecipientIDs = append(p.RecipientIDs, recipient)
				}
			}
			if p.Visibility == "direct" && len(p.RecipientIDs) == 0 {
				p.ArchiveOnly = true
				parserIssue(b, "warning", "missing_recipients", "post", p.ID, "Private note has no resolvable recipient addresses; it remains an archive only.")
			}
			for j, attachment := range arr(note["attachment"]) {
				m := obj(attachment)
				uri := text(m["url"])
				mediaID := p.ID + "#media-" + fmt.Sprint(j)
				media := Media{ID: mediaID, OwnerID: author, Name: text(m["name"]), ContentType: text(m["mediaType"]), Description: text(m["name"]), Blurhash: text(m["blurhash"]), Width: int(integer(m["width"])), Height: int(integer(m["height"]))}
				if uri != "" {
					if _, ok := a.Files[uri]; ok {
						media.Path = uri
					} else {
						u, _ := url.Parse(uri)
						if u != nil && u.IsAbs() {
							media.RemoteURL = uri
						} else {
							media.Path = uri
						}
					}
				}
				b.Media = append(b.Media, media)
				p.MediaIDs = append(p.MediaIDs, mediaID)
			}
			for _, tag := range arr(note["tag"]) {
				t := obj(tag)
				switch text(t["type"]) {
				case "Hashtag":
					p.Tags = append(p.Tags, strings.TrimPrefix(text(t["name"]), "#"))
				case "Emoji":
					id := text(t["id"])
					if id == "" {
						id = text(obj(t["icon"])["url"])
					}
					p.EmojiIDs = append(p.EmojiIDs, id)
					b.Emojis = append(b.Emojis, Emoji{ID: id, Shortcode: strings.Trim(text(t["name"]), ":"), URL: text(obj(t["icon"])["url"])})
				}
			}
			options := arr(note["oneOf"])
			multiple := false
			if len(options) == 0 {
				options = arr(note["anyOf"])
				multiple = true
			}
			if len(options) > 0 {
				poll := Poll{ID: p.ID + "#poll", PostID: p.ID, Multiple: multiple}
				for _, v := range options {
					o := obj(v)
					poll.Options = append(poll.Options, PollOption{Title: text(o["name"]), Votes: uint64(max(0, integer(obj(o["replies"])["totalItems"])))})
				}
				expires := timestamp(note["endTime"])
				if !expires.IsZero() {
					poll.ExpiresAt = &expires
				}
				b.Polls = append(b.Polls, poll)
			}
			b.Posts = append(b.Posts, p)
		}
	}
	for _, kind := range []string{"bookmarks", "likes"} {
		name := kind + ".json"
		if _, ok := a.Files[name]; !ok {
			continue
		}
		var c map[string]any
		if err := readJSON(a, name, &c); err != nil {
			return nil, err
		}
		for i, v := range arr(c["orderedItems"]) {
			uri := text(v)
			if uri == "" {
				continue
			}
			if kind == "bookmarks" {
				b.Bookmarks = append(b.Bookmarks, Bookmark{AccountID: owner, PostURI: uri})
			} else {
				b.Reactions = append(b.Reactions, Reaction{ID: "like:" + fmt.Sprint(i), AccountID: owner, PostID: uri})
			}
		}
	}
	for name := range a.Files {
		if strings.HasSuffix(strings.ToLower(name), ".csv") {
			if err := parseCSV(a, name, inferKind(name), b); err != nil {
				return nil, err
			}
		}
	}
	parserIssue(b, "info", "source_export_limits", "backup", "", "Mastodon archives contain the author's exported posts and media; incoming messages and separately exported relationship CSV files cannot be invented.")
	normalizePortableReferences(b)
	if err := validateBundle(a, b); err != nil {
		return nil, err
	}
	return b, nil
}
