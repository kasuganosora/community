package datatransfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

func stageImportMedia(ctx context.Context, b *Bundle, o Options) (map[string]string, func(), error) {
	paths := map[string]string{}
	var created []string
	cleanup := func() {
		for _, p := range created {
			_ = os.Remove(p)
		}
	}
	root := o.StorageDir
	if root == "" {
		root = filepath.Join("uploads", "media")
	}
	for _, m := range b.Media {
		if m.Path == "" || (o.Scope == "personal" && m.OwnerID != b.OwnerID) {
			continue
		}
		if err := ctx.Err(); err != nil {
			cleanup()
			return nil, cleanup, err
		}
		file, ok := o.Files[m.Path]
		if !ok {
			cleanup()
			return nil, cleanup, errors.New("unverified media path")
		}
		info, err := os.Lstat(file.Path)
		if err != nil || !info.Mode().IsRegular() {
			cleanup()
			return nil, cleanup, errors.New("invalid verified media file")
		}
		src, err := os.Open(file.Path)
		if err != nil {
			cleanup()
			return nil, cleanup, errors.New("cannot read media file")
		}
		relative := "imports/" + uuid.NewString()
		target := filepath.Join(root, filepath.FromSlash(relative))
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			src.Close()
			cleanup()
			return nil, cleanup, err
		}
		dst, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			src.Close()
			cleanup()
			return nil, cleanup, err
		}
		created = append(created, target)
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(dst, hash), archiveContextReader{ctx: ctx, r: src})
		closeErr := dst.Close()
		src.Close()
		if copyErr != nil || closeErr != nil || n != file.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), file.SHA256) {
			cleanup()
			return nil, cleanup, errors.New("media checksum verification failed")
		}
		paths[m.ID] = relative
	}
	return paths, cleanup, nil
}
func (i *importer) importMedia() error {
	for _, m := range i.bundle.Media {
		if i.personal() && m.OwnerID != i.bundle.OwnerID {
			if err := i.preserve("media", m.ID, m); err != nil {
				return err
			}
			continue
		}
		created := false
		targetID, err := i.apply("media", m.ID, m, func(tx *gorm.DB) (uint64, error) {
			owner, err := i.ref("account", sharedMediaOwner(i.bundle, m))
			if err != nil {
				return 0, err
			}
			target := models.MediaAttachment{AccountId: owner, FilePath: i.mediaPaths[m.ID], FileName: filepath.Base(i.mediaPaths[m.ID]), FileSize: m.Size, ContentType: m.ContentType, Type: models.GetMediaType(m.ContentType), RemoteUrl: m.RemoteURL, Description: m.Description, Blurhash: m.Blurhash, FocusX: m.FocusX, FocusY: m.FocusY, CreatedAt: m.CreatedAt}
			if target.FilePath == "" {
				target.FileName = m.Name
			}
			if file, ok := i.options.Files[m.Path]; ok {
				target.FileSize = file.Size
			}
			if m.Width > 0 {
				target.Width = &m.Width
			}
			if m.Height > 0 {
				target.Height = &m.Height
			}
			err = tx.Create(&target).Error
			created = err == nil
			return target.Id, err
		})
		if err != nil {
			return err
		}
		if err = RecordMediaAlias(i.tx, i.options, m, targetID); err != nil {
			return err
		}
		if !created && i.mediaPaths[m.ID] != "" {
			root := i.options.StorageDir
			if root == "" {
				root = filepath.Join("uploads", "media")
			}
			_ = os.Remove(filepath.Join(root, filepath.FromSlash(i.mediaPaths[m.ID])))
		}
		if err = i.preserve("media_metadata", m.ID, m); err != nil {
			return err
		}
	}
	return i.importAccountImages()
}

func sharedMediaOwner(b *Bundle, m Media) string {
	if m.OwnerID != "" {
		return m.OwnerID
	}
	shared := false
	for _, e := range b.Emojis {
		if e.MediaID == m.ID {
			shared = true
		}
	}
	for _, a := range b.Accounts {
		if a.AvatarID == m.ID || a.HeaderID == m.ID {
			shared = true
		}
	}
	if !shared {
		return ""
	}
	for _, a := range b.Accounts {
		if a.Local && a.System {
			return a.ID
		}
	}
	for _, a := range b.Accounts {
		if a.Local {
			return a.ID
		}
	}
	return ""
}
