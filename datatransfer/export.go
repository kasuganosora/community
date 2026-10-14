package datatransfer

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"gorm.io/gorm"
)

func exportID(id uint64) string { return strconv.FormatUint(id, 10) }
func exportTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
func exportPointer(id *uint64) string {
	if id == nil {
		return ""
	}
	return exportID(*id)
}
func exportActor(a *models.Account, baseURL string) string {
	if a.OriginId != "" {
		return a.OriginId
	}
	if a.LocalActorURI != "" {
		return a.LocalActorURI
	}
	return strings.TrimRight(baseURL, "/") + "/users/" + url.PathEscape(a.Username)
}
func exportPostURI(p *models.Status, baseURL string) string {
	if p.LocalObjectURI != "" {
		return p.LocalObjectURI
	}
	if p.OriginUri != nil && *p.OriginUri != "" {
		return *p.OriginUri
	}
	if p.Account != nil {
		return exportActor(p.Account, baseURL) + "/statuses/" + exportID(p.Id)
	}
	return ""
}

type exportMediaFile struct {
	index       int
	storagePath string
}

// ExportPersonal emits a versioned portable ZIP for exactly one authenticated
// local account. Keys, password hashes, email secrets, sessions and admin status
// never enter the manifest. Received DMs are private archival records, not new
// authored posts. The caller must authorize accountID against the session.
func ExportPersonal(ctx context.Context, conn *gorm.DB, accountID uint64, baseURL string, w io.Writer) error {
	if accountID == 0 {
		return errors.New("account is required")
	}
	var owner models.Account
	conn = conn.WithContext(ctx)
	if err := conn.Preload("Extra").Preload("Emojis").First(&owner, accountID).Error; err != nil {
		return err
	}
	if !owner.IsLocal() {
		return errors.New("personal export requires a local account")
	}
	b := &Bundle{Schema: Schema, Scope: "personal", OwnerID: exportID(owner.Id), Source: Source{Platform: "community", BaseURL: strings.TrimRight(baseURL, "/"), ExportedAt: time.Now().UTC()}}
	accountIDs := map[uint64]bool{owner.Id: true}
	profile := Account{ID: b.OwnerID, Username: owner.Username, ActorURI: exportActor(&owner, baseURL), URL: strings.TrimRight(baseURL, "/") + "/@" + owner.Username, Local: true, DisplayName: owner.DisplayName, Note: owner.Note, Locked: owner.Locked, Bot: owner.Bot, Discoverable: owner.Discoverable, NoIndex: owner.NoIndex, HideCollections: owner.HideCollections, CreatedAt: exportTime(owner.CreatedAt), UpdatedAt: exportTime(owner.UpdatedAt)}
	if owner.ImportedNoteHTML != "" {
		profile.Note = owner.ImportedNoteHTML
		profile.NoteHTML = true
	}
	if owner.Extra != nil {
		for _, f := range owner.Extra.Fields {
			profile.Fields = append(profile.Fields, Field{Name: f.Name, Value: f.Val})
		}
	}
	b.Accounts = append(b.Accounts, profile)
	files := []exportMediaFile{}
	mediaSeen := map[uint64]bool{}
	emojiSeen := map[uint64]bool{}
	for _, emoji := range owner.Emojis {
		b.Accounts[0].EmojiIDs = append(b.Accounts[0].EmojiIDs, exportID(emoji.Id))
		if !emojiSeen[emoji.Id] {
			emojiSeen[emoji.Id] = true
			b.Emojis = append(b.Emojis, Emoji{ID: exportID(emoji.Id), Shortcode: emoji.Shortcode, Domain: emoji.Domain, URL: emoji.Url, Category: emoji.Category, Aliases: emoji.Aliases, Disabled: emoji.Disabled, Sensitive: emoji.IsSensitive, License: emoji.License})
		}
	}
	addMedia := func(m *models.MediaAttachment) {
		if mediaSeen[m.Id] {
			return
		}
		mediaSeen[m.Id] = true
		accountIDs[m.AccountId] = true
		v := Media{ID: exportID(m.Id), OwnerID: exportID(m.AccountId), Name: m.FileName, ContentType: m.ContentType, Size: m.FileSize, RemoteURL: m.RemoteUrl, Description: m.Description, Blurhash: m.Blurhash, FocusX: m.FocusX, FocusY: m.FocusY, CreatedAt: m.CreatedAt}
		if m.Width != nil {
			v.Width = *m.Width
		}
		if m.Height != nil {
			v.Height = *m.Height
		}
		b.Media = append(b.Media, v)
		if m.FilePath != "" {
			files = append(files, exportMediaFile{len(b.Media) - 1, m.StorageKey()})
		}
	}
	lastID := uint64(0)
	exported := map[uint64]bool{}
	limits := DefaultLimits()
	for {
		var rows models.Statuses
		query := conn.Where("statuses.id > ? AND statuses.deleted_at IS NULL", lastID).Where("statuses.account_id = ? OR (statuses.visibility = ? AND statuses.id IN (SELECT status_id FROM status_to_accounts WHERE account_id = ?))", accountID, models.StatusVisibilityDirect, accountID).Order("statuses.id ASC").Limit(250)
		if err := query.Preload("Content").Preload("Account").Preload("Attachments").Preload("ToAccounts").Preload("Emojis").Preload("Tags").Preload("Poll.Options").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		lastID = rows[len(rows)-1].Id
		visible, err := models.FilterVisibleStatuses(conn, &owner, rows)
		if err != nil {
			return err
		}
		for _, s := range visible {
			accountIDs[s.AccountId] = true
			exported[s.Id] = true
			p := Post{ID: exportID(s.Id), AuthorID: exportID(s.AccountId), URI: exportPostURI(s, baseURL), Text: s.Content.Content, SpoilerText: s.Content.SpoilerText, Visibility: []string{"public", "unlisted", "private", "direct"}[min(int(s.Visibility), 3)], Sensitive: s.Sensitive, Language: s.Language, ReplyID: exportPointer(s.InReplyToId), ReblogID: exportPointer(s.ReblogId), ConversationID: exportPointer(s.ConversationId), CreatedAt: exportTime(s.CreatedAt), UpdatedAt: exportTime(s.UpdatedAt), ArchiveOnly: s.AccountId != accountID}
			p.LocalOnly = s.LocalOnly
			if original, ok := s.Properties["import_original_html"].(string); ok && original != "" {
				p.HTML = original
				p.Text = ""
			}
			if s.Domain != nil {
				p.HTML = p.Text
				p.Text = ""
			}
			if s.OriginUrl != nil {
				p.URL = *s.OriginUrl
			}
			if local, ok := s.Properties["local_only"].(bool); ok {
				p.LocalOnly = p.LocalOnly || local
			}
			for _, to := range s.ToAccounts {
				accountIDs[to.Id] = true
				p.RecipientIDs = append(p.RecipientIDs, exportID(to.Id))
			}
			for _, m := range s.Attachments {
				addMedia(m)
				p.MediaIDs = append(p.MediaIDs, exportID(m.Id))
			}
			for _, tag := range s.Tags {
				p.Tags = append(p.Tags, tag.Name)
			}
			for _, emoji := range s.Emojis {
				p.EmojiIDs = append(p.EmojiIDs, exportID(emoji.Id))
				if !emojiSeen[emoji.Id] {
					emojiSeen[emoji.Id] = true
					b.Emojis = append(b.Emojis, Emoji{ID: exportID(emoji.Id), Shortcode: emoji.Shortcode, Domain: emoji.Domain, URL: emoji.Url, Category: emoji.Category, Aliases: emoji.Aliases, Disabled: emoji.Disabled, Sensitive: emoji.IsSensitive, License: emoji.License})
				}
			}
			if s.Poll != nil {
				poll := Poll{ID: exportID(s.Poll.Id), PostID: p.ID, Multiple: s.Poll.Multiple, ExpiresAt: s.Poll.ExpiresAt}
				for _, o := range s.Poll.Options.SortByIndex() {
					poll.Options = append(poll.Options, PollOption{Title: o.Content, Votes: o.Votes})
				}
				b.Polls = append(b.Polls, poll)
			}
			b.Posts = append(b.Posts, p)
			if len(b.Posts) > limits.MaxRecords {
				return errors.New("personal backup exceeds record limit")
			}
		}
	}
	// Include the owner's unattached uploads, but never arbitrary third-party files.
	var uploads []*models.MediaAttachment
	if err := conn.Where("account_id = ?", accountID).Order("id").Find(&uploads).Error; err != nil {
		return err
	}
	for _, m := range uploads {
		addMedia(m)
	}
	if err := exportPersonalCollections(conn, &owner, b, accountIDs, exported, baseURL); err != nil {
		return err
	}
	ids := []uint64{}
	for id := range accountIDs {
		if id != owner.Id {
			ids = append(ids, id)
		}
	}
	if len(ids) > 0 {
		var references []*models.Account
		if err := conn.Where("id IN ?", ids).Find(&references).Error; err != nil {
			return err
		}
		for _, a := range references {
			domain := ""
			if a.Domain != nil {
				domain = *a.Domain
			} else {
				u, _ := url.Parse(baseURL)
				domain = u.Host
			}
			b.Accounts = append(b.Accounts, Account{ID: exportID(a.Id), Username: a.Username, Domain: domain, ActorURI: exportActor(a, baseURL), DisplayName: a.DisplayName, Local: false})
		}
	}
	for _, asset := range []struct{ kind, value string }{{"avatar", owner.Avatar}, {"header", owner.Header}} {
		if asset.value == "" {
			continue
		}
		id := "profile:" + asset.kind
		m := Media{ID: id, OwnerID: b.OwnerID, Name: path.Base(asset.value)}
		if strings.HasPrefix(asset.value, "http://") || strings.HasPrefix(asset.value, "https://") {
			m.RemoteURL = asset.value
			sourceURL, e := url.Parse(asset.value)
			base, e2 := url.Parse(baseURL)
			if e == nil && e2 == nil && sourceURL.Scheme == base.Scheme && sourceURL.Host == base.Host && sourceURL.User == nil {
				parts := strings.Split(strings.TrimPrefix(sourceURL.Path, "/"), "/")
				if len(parts) == 3 && parts[0] == "media" {
					mediaID, e := strconv.ParseUint(parts[1], 10, 64)
					if e == nil {
						var existing models.MediaAttachment
						if err := conn.Where("id = ? AND account_id = ?", mediaID, accountID).First(&existing).Error; err == nil && parts[2] == existing.FileName && existing.FilePath != "" {
							m.Name = existing.FileName
							m.ContentType = existing.ContentType
							m.RemoteURL = ""
							files = append(files, exportMediaFile{len(b.Media), existing.StorageKey()})
						} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
							return err
						}
					}
				}
			}
		} else {
			files = append(files, exportMediaFile{len(b.Media), asset.value})
		}
		b.Media = append(b.Media, m)
		if asset.kind == "avatar" {
			b.Accounts[0].AvatarID = id
			b.Accounts[0].AvatarURL = m.RemoteURL
		} else {
			b.Accounts[0].HeaderID = id
			b.Accounts[0].HeaderURL = m.RemoteURL
		}
	}
	normalizePortableReferences(b)
	if len(files)+1 > limits.MaxFiles {
		return errors.New("personal backup exceeds file count limit")
	}
	if len(b.Posts)+len(b.Media)+len(b.Accounts)+len(b.Records)+len(b.Relations)+len(b.Reactions)+len(b.Bookmarks)+len(b.Polls)+len(b.PollVotes)+len(b.Lists)+len(b.Emojis) > limits.MaxRecords {
		return errors.New("personal backup exceeds record count limit")
	}
	zw := zip.NewWriter(&limitedBackupWriter{w: w, remaining: limits.MaxCompressedBytes})
	var expanded int64
	for i, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		m := &b.Media[file.index]
		if file.storagePath == "" {
			continue
		}
		reader, err := openExportMedia(file.storagePath)
		if err != nil {
			return errors.New("a stored media file is unavailable; the personal backup cannot be completed")
		}
		buffered := bufio.NewReader(reader)
		if m.ContentType == "" {
			sample, _ := buffered.Peek(512)
			m.ContentType = http.DetectContentType(sample)
		}
		archivePath := fmt.Sprintf("media/%06d/blob", i)
		entry, err := zw.Create(archivePath)
		if err != nil {
			reader.Close()
			return err
		}
		h := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(entry, h), io.LimitReader(archiveContextReader{ctx, buffered}, limits.MaxFileBytes+1))
		reader.Close()
		if copyErr != nil {
			return copyErr
		}
		expanded += n
		if n > limits.MaxFileBytes || expanded > limits.MaxExpandedBytes {
			return errors.New("personal backup media exceeds size limit")
		}
		m.Path = archivePath
		m.Size = n
		m.SHA256 = hex.EncodeToString(h.Sum(nil))
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if int64(len(payload)) > limits.MaxJSONBytes {
		return errors.New("personal backup manifest exceeds size limit")
	}
	entry, err := zw.Create("bundle.json")
	if err != nil {
		return err
	}
	if _, err = entry.Write(payload); err != nil {
		return err
	}
	return zw.Close()
}

// openExportMedia reads a stored object (local uploads/ or S3) by key.
func openExportMedia(p string) (io.ReadCloser, error) {
	p = strings.TrimPrefix(p, "uploads/")
	if _, err := SafeArchiveName(p); err != nil {
		return nil, err
	}
	rc, _, err := storage.Default().Open(context.Background(), p)
	return rc, err
}

type limitedBackupWriter struct {
	w         io.Writer
	remaining int64
}

func (w *limitedBackupWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		return 0, errors.New("compressed backup exceeds upload limit")
	}
	n, e := w.w.Write(p)
	w.remaining -= int64(n)
	return n, e
}
