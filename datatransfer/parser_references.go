package datatransfer

// Foreign exports omit targets outside the author's own outbox. Preserve the
// original references privately rather than inventing remote posts or actors.
func normalizePortableReferences(b *Bundle) {
	posts := map[string]bool{}
	media := map[string]bool{}
	accounts := map[string]bool{}
	for _, v := range b.Posts {
		posts[v.ID] = true
	}
	for _, v := range b.Media {
		media[v.ID] = true
	}
	for _, v := range b.Accounts {
		accounts[v.ID] = true
	}
	for i := range b.Posts {
		p := &b.Posts[i]
		original := *p
		missing := false
		if p.ReplyID != "" && !posts[p.ReplyID] {
			p.ReplyID = ""
			missing = true
		}
		if p.ReblogID != "" && !posts[p.ReblogID] {
			p.ReblogID = ""
			p.ArchiveOnly = true
			missing = true
		}
		recipients := []string{}
		for _, id := range p.RecipientIDs {
			if accounts[id] {
				recipients = append(recipients, id)
			} else {
				missing = true
				p.ArchiveOnly = true
			}
		}
		p.RecipientIDs = recipients
		attached := []string{}
		for _, id := range p.MediaIDs {
			if media[id] {
				attached = append(attached, id)
			} else {
				missing = true
				p.ArchiveOnly = true
			}
		}
		p.MediaIDs = attached
		if missing {
			b.Records = append(b.Records, PreservedRecord{Kind: "original_post_references", ID: p.ID, OwnerID: b.OwnerID, Data: raw(original)})
			parserIssue(b, "warning", "missing_source_references", "post", p.ID, "Source references absent from the backup are retained privately; no target records were invented.")
		}
	}
	reactions := []Reaction{}
	for _, r := range b.Reactions {
		if posts[r.PostID] {
			reactions = append(reactions, r)
		} else {
			b.Records = append(b.Records, PreservedRecord{Kind: "reaction", ID: r.ID, OwnerID: b.OwnerID, Data: raw(r)})
			parserIssue(b, "info", "archived_reaction", "reaction", r.ID, "Reaction target is outside the supplied backup; the reaction is preserved privately.")
		}
	}
	b.Reactions = reactions
	unique := []Emoji{}
	seen := map[string]bool{}
	for _, v := range b.Emojis {
		if !seen[v.ID] {
			seen[v.ID] = true
			unique = append(unique, v)
		}
	}
	b.Emojis = unique
}
