package datatransfer

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ParseOptions struct{ Platform, Kind string }

func (a *Archive) Root() string { return a.root }
func readJSON(a *Archive, name string, target any) error {
	b, e := a.Read(name, a.limits.MaxJSONBytes)
	if e != nil {
		return e
	}
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if e = d.Decode(target); e != nil {
		return fmt.Errorf("invalid JSON in %s: %w", name, e)
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("multiple JSON values in backup")
	}
	return nil
}
func ParseArchive(ctx context.Context, a *Archive, opts ParseOptions) (*Bundle, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	for _, name := range []string{"bundle.json", "manifest.json"} {
		if _, ok := a.Files[name]; ok {
			var b Bundle
			if err := readJSON(a, name, &b); err != nil {
				return nil, err
			}
			if b.Schema != Schema {
				return nil, errors.New("unsupported backup schema")
			}
			if b.Scope != "personal" && b.Scope != "site" {
				return nil, errors.New("invalid backup scope")
			}
			if err := validateBundle(a, &b); err != nil {
				return nil, err
			}
			return &b, nil
		}
	}
	names := make([]string, 0, len(a.Files))
	for n := range a.Files {
		names = append(names, n)
	}
	sort.Strings(names)
	platform := strings.ToLower(opts.Platform)
	if platform == "auto" {
		platform = ""
	}
	if _, ok := a.Files["actor.json"]; ok && (platform == "" || platform == "mastodon") {
		return parseMastodon(ctx, a)
	}
	b := &Bundle{Schema: Schema, Scope: "personal", Source: Source{Platform: platform}, OwnerID: "owner", Accounts: []Account{{ID: "owner", Local: true}}}
	if platform == "" {
		platform = "misskey"
		b.Source.Platform = platform
	}
	if platform != "misskey" && platform != "mastodon" {
		return nil, errors.New("unrecognized backup format")
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		kind := opts.Kind
		if len(names) > 1 || kind == "" || kind == "archive" {
			kind = inferKind(name)
		}
		switch strings.ToLower(path.Ext(name)) {
		case ".csv":
			if kind == "" {
				return nil, errors.New("CSV backup requires an explicit kind")
			}
			if err := parseCSV(a, name, kind, b); err != nil {
				return nil, err
			}
		case ".json":
			if platform == "misskey" && (kind == "notes" || kind == "posts" || kind == "") {
				if err := parseMisskeyNotes(a, name, b); err != nil {
					return nil, err
				}
			} else {
				raw, err := a.Read(name, a.limits.MaxJSONBytes)
				if err != nil {
					return nil, err
				}
				if !json.Valid(raw) {
					return nil, errors.New("invalid JSON export")
				}
				b.Records = append(b.Records, PreservedRecord{Kind: kind, ID: name, OwnerID: b.OwnerID, Data: raw})
				parserIssue(b, "warning", "archive_only", kind, name, "This source collection is preserved privately; it is not converted into a live feature.")
			}
		default:
			continue
		}
	}
	if len(b.Posts)+len(b.Relations)+len(b.Lists)+len(b.Bookmarks)+len(b.Records) == 0 {
		return nil, errors.New("backup has no recognized user data")
	}
	normalizePortableReferences(b)
	if err := validateBundle(a, b); err != nil {
		return nil, err
	}
	return b, nil
}
func inferKind(name string) string {
	n := strings.ToLower(path.Base(name))
	for _, pair := range [][2]string{{"notes", "notes"}, {"outbox", "posts"}, {"following", "following"}, {"blocked_domains", "domain_blocks"}, {"blocked", "blocks"}, {"blocking", "blocks"}, {"muted", "mutes"}, {"mute", "mutes"}, {"muting", "mutes"}, {"user-lists", "lists"}, {"lists", "lists"}, {"bookmarks", "bookmarks"}, {"favorites", "favorites"}, {"likes", "likes"}, {"filters", "filters"}, {"clips", "clips"}, {"antennas", "antennas"}} {
		if strings.HasPrefix(n, pair[0]) {
			return pair[1]
		}
	}
	return ""
}
func parserIssue(b *Bundle, severity, code, kind, id, message string) {
	b.Issues = append(b.Issues, Issue{Severity: severity, Code: code, Kind: kind, SourceID: id, Message: message})
}
func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	}
	return ""
}
func obj(v any) map[string]any { x, _ := v.(map[string]any); return x }
func arr(v any) []any          { x, _ := v.([]any); return x }
func stringsOf(v any) []string {
	if s := text(v); s != "" {
		return []string{s}
	}
	out := []string{}
	for _, x := range arr(v) {
		if s := text(x); s != "" {
			out = append(out, s)
		}
	}
	return out
}
func boolean(v any) bool        { x, _ := v.(bool); return x }
func integer(v any) int64       { i, _ := strconv.ParseInt(text(v), 10, 64); return i }
func timestamp(v any) time.Time { t, _ := time.Parse(time.RFC3339Nano, text(v)); return t }
func raw(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func origin(uri string) string {
	u, e := url.Parse(uri)
	if e != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
func accountRef(b *Bundle, id string) {
	if id == "" {
		return
	}
	for _, a := range b.Accounts {
		if a.ID == id {
			return
		}
	}
	u, _ := url.Parse(id)
	domain := ""
	if u != nil {
		domain = u.Host
	}
	actorURI := id
	username := ""
	if domain == "" {
		parts := strings.Split(strings.TrimPrefix(id, "@"), "@")
		if len(parts) == 2 {
			username = parts[0]
			domain = parts[1]
			actorURI = ""
		}
	}
	b.Accounts = append(b.Accounts, Account{ID: id, ActorURI: actorURI, Username: username, Domain: domain, Local: false})
}
func validateBundle(a *Archive, b *Bundle) error {
	count := len(b.Accounts) + len(b.Posts) + len(b.Media) + len(b.Relations) + len(b.Lists) + len(b.Reactions) + len(b.Bookmarks) + len(b.Polls) + len(b.PollVotes) + len(b.Emojis) + len(b.Records)
	if count > a.limits.MaxRecords {
		return errors.New("backup has too many records")
	}
	seen := map[string]bool{}
	for _, p := range b.Posts {
		if p.ID == "" || seen[p.ID] {
			return errors.New("post source IDs must be nonempty and unique")
		}
		seen[p.ID] = true
	}
	seen = map[string]bool{}
	for i := range b.Media {
		m := &b.Media[i]
		if m.ID == "" || seen[m.ID] {
			return errors.New("media source IDs must be nonempty and unique")
		}
		seen[m.ID] = true
		if m.Path != "" {
			name, err := SafeArchiveName(m.Path)
			if err != nil {
				return err
			}
			f, ok := a.Files[name]
			if !ok {
				parserIssue(b, "warning", "missing_media", "media", m.ID, "The referenced media bytes are absent from the uploaded backup.")
				m.Path = ""
				continue
			}
			if m.SHA256 != "" && !strings.EqualFold(m.SHA256, f.SHA256) {
				return errors.New("backup media checksum mismatch")
			}
			if m.Size != 0 && m.Size != f.Size {
				return errors.New("backup media size mismatch")
			}
			m.Size = f.Size
			m.SHA256 = f.SHA256
		}
	}
	return nil
}
func parseCSV(a *Archive, name, kind string, b *Bundle) error {
	data, err := a.Read(name, a.limits.MaxJSONBytes)
	if err != nil {
		return err
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), "\ufeff")))
	r.FieldsPerRecord = -1
	lists := map[string]int{}
	line := 0
	for {
		row, e := r.Read()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		line++
		if line > a.limits.MaxRecords {
			return errors.New("CSV record limit exceeded")
		}
		if len(row) == 0 || strings.TrimSpace(row[0]) == "" {
			continue
		}
		first := strings.TrimSpace(row[0])
		if line == 1 && strings.EqualFold(first, "Account address") {
			continue
		}
		switch kind {
		case "following", "follows", "blocks", "blocking", "mutes", "muting", "domain_blocks", "blocked_domains":
			relationKind := map[string]string{"following": "follow", "follows": "follow", "blocks": "block", "blocking": "block", "mutes": "mute", "muting": "mute", "domain_blocks": "domain_block", "blocked_domains": "domain_block"}[kind]
			rel := Relation{Kind: relationKind, AccountID: b.OwnerID, ShowReblogs: true}
			if relationKind == "domain_block" {
				rel.TargetDomain = first
			} else {
				rel.TargetID = first
				rel.TargetURI = first
				accountRef(b, first)
			}
			if b.Source.Platform == "mastodon" && relationKind == "follow" {
				if len(row) > 1 {
					rel.ShowReblogs = !strings.EqualFold(row[1], "false")
				}
				if len(row) > 2 {
					rel.Notify = strings.EqualFold(row[2], "true")
				}
			}
			b.Relations = append(b.Relations, rel)
			if len(row) > 1 {
				b.Records = append(b.Records, PreservedRecord{Kind: "relation_options", ID: name + ":" + strconv.Itoa(line), OwnerID: b.OwnerID, Data: raw(row)})
			}
		case "bookmarks":
			b.Bookmarks = append(b.Bookmarks, Bookmark{AccountID: b.OwnerID, PostURI: first})
		case "lists":
			if len(row) < 2 {
				return errors.New("list CSV row requires title and account")
			}
			i, ok := lists[first]
			if !ok {
				i = len(b.Lists)
				lists[first] = i
				b.Lists = append(b.Lists, List{ID: name + ":" + strconv.Itoa(i), OwnerID: b.OwnerID, Title: first})
			}
			target := strings.TrimSpace(row[1])
			accountRef(b, target)
			b.Lists[i].MemberIDs = append(b.Lists[i].MemberIDs, target)
		default:
			return fmt.Errorf("unsupported CSV kind %q", kind)
		}
	}
	return nil
}
