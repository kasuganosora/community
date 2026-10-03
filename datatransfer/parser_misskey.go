package datatransfer

import (
	"errors"
	"fmt"
)

func parseMisskeyNotes(a *Archive, name string, b *Bundle) error {
	var notes []map[string]any
	if err := readJSON(a, name, &notes); err != nil {
		return err
	}
	if len(notes) > a.limits.MaxRecords {
		return errors.New("note record limit exceeded")
	}
	mediaSeen := map[string]bool{}
	for _, m := range b.Media {
		mediaSeen[m.ID] = true
	}
	for _, n := range notes {
		p := Post{ID: text(n["id"]), AuthorID: b.OwnerID, Text: text(n["text"]), SpoilerText: text(n["cw"]), CreatedAt: timestamp(n["createdAt"]), ReplyID: text(n["replyId"]), ReblogID: text(n["renoteId"]), LocalOnly: boolean(n["localOnly"]), Visibility: text(n["visibility"]), Extensions: raw(n)}
		if p.ID == "" {
			return errors.New("Misskey note source ID is missing")
		}
		switch p.Visibility {
		case "public":
		case "home":
			p.Visibility = "unlisted"
		case "followers":
			p.Visibility = "private"
		case "specified":
			p.Visibility = "direct"
			p.ArchiveOnly = true
			parserIssue(b, "warning", "unresolved_recipient_ids", "post", p.ID, "Misskey exports contain instance-local visibleUserIds, not portable actor addresses; this private note is archived without guessing recipients.")
		default:
			p.Visibility = "direct"
			p.ArchiveOnly = true
			parserIssue(b, "warning", "unknown_visibility", "post", p.ID, "Unknown source visibility is retained as a private archive, never made public.")
		}
		p.RecipientIDs = stringsOf(n["visibleUserIds"])
		for _, file := range arr(n["files"]) {
			f := obj(file)
			id := text(f["id"])
			if id == "" {
				return errors.New("Misskey drive file ID missing")
			}
			p.MediaIDs = append(p.MediaIDs, id)
			if mediaSeen[id] {
				continue
			}
			mediaSeen[id] = true
			props := obj(f["properties"])
			m := Media{ID: id, OwnerID: b.OwnerID, Name: text(f["name"]), ContentType: text(f["type"]), RemoteURL: text(f["url"]), Size: integer(f["size"]), Description: text(f["comment"]), Blurhash: text(f["blurhash"]), Width: int(integer(props["width"])), Height: int(integer(props["height"])), CreatedAt: timestamp(f["createdAt"])}
			if filePath := text(f["path"]); filePath != "" {
				m.Path = filePath
			}
			b.Media = append(b.Media, m)
		}
		for _, id := range stringsOf(n["fileIds"]) {
			found := false
			for _, existing := range p.MediaIDs {
				if existing == id {
					found = true
				}
			}
			if !found {
				p.MediaIDs = append(p.MediaIDs, id)
				parserIssue(b, "warning", "missing_media_metadata", "media", id, "The source references a drive file absent from the export.")
			}
		}
		if poll := obj(n["poll"]); poll != nil {
			v := Poll{ID: p.ID + "#poll", PostID: p.ID, Multiple: boolean(poll["multiple"])}
			votes := arr(poll["votes"])
			for i, title := range arr(poll["choices"]) {
				count := int64(0)
				if i < len(votes) {
					count = integer(votes[i])
				}
				v.Options = append(v.Options, PollOption{Title: text(title), Votes: uint64(max(0, count))})
			}
			expires := timestamp(poll["expiresAt"])
			if !expires.IsZero() {
				v.ExpiresAt = &expires
			}
			b.Polls = append(b.Polls, v)
		}
		if p.ReblogID != "" && p.Text == "" && len(p.MediaIDs) == 0 {
			p.ArchiveOnly = true
			parserIssue(b, "warning", "archive_only", "post", p.ID, "An exported pure renote is retained without re-announcing its author.")
		}
		b.Posts = append(b.Posts, p)
	}
	parserIssue(b, "info", "source_export_limits", "backup", fmt.Sprint(name), "Misskey note exports do not contain a profile, login identity, incoming chat history, or media bytes unless those are supplied separately. Remote media URLs have not been fetched.")
	return nil
}
