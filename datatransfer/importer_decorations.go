package datatransfer

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// maxDecorationNameRunes matches models.AvatarDecoration.Name (size:80).
const maxDecorationNameRunes = 80

// importAvatarDecorations installs the source instance's avatar decoration
// definitions as local decorations (image from the imported media file) and
// then restores every account's selected decorations with their geometry.
//
// Re-runs and final syncs are idempotent: a definition's identity receipt is
// keyed by its source id only, while its content and each account's
// selection are keyed by a digest of the value, so unchanged data is skipped
// and changed data is applied again.
func (i *importer) importAvatarDecorations() error {
	if i.personal() {
		// Decorations are instance-wide; a personal archive cannot install them.
		return nil
	}
	descriptions := 0
	for _, d := range i.bundle.AvatarDecorations {
		if d.ID == "" {
			continue
		}
		if strings.TrimSpace(d.Description) != "" {
			descriptions++
		}
		imageURL, err := i.decorationImageURL(d)
		if err != nil {
			return err
		}
		if imageURL == "" {
			i.report.Issues = append(i.report.Issues, Issue{Severity: "warning", Code: "avatar_decoration_image_unavailable", Kind: "avatar_decoration", SourceID: d.ID,
				Message: "avatar decoration image was not imported; the decoration and selections using it are skipped"})
			continue
		}
		name := strings.TrimSpace(d.Name)
		if name == "" {
			name = "decoration"
		}
		if utf8.RuneCountInString(name) > maxDecorationNameRunes {
			name = truncateRunes(name, maxDecorationNameRunes)
		}
		target, err := i.apply("avatar_decoration", d.ID, struct{ ID string }{d.ID}, func(tx *gorm.DB) (uint64, error) {
			row := models.AvatarDecoration{Name: name, ImageURL: imageURL, Enabled: true}
			err := tx.Create(&row).Error
			return row.ID, err
		})
		if err != nil {
			return err
		}
		content := struct{ Name, ImageURL string }{name, imageURL}
		digest, err := digestValue(content)
		if err != nil {
			return err
		}
		if _, err = i.apply("avatar_decoration_content", d.ID+"\n"+digest, content, func(tx *gorm.DB) (uint64, error) {
			err := tx.Model(&models.AvatarDecoration{}).Where("id = ?", target).Updates(map[string]any{"name": name, "image_url": imageURL}).Error
			return target, err
		}); err != nil {
			return err
		}
	}
	if descriptions > 0 {
		i.report.Issues = append(i.report.Issues, Issue{Severity: "info", Code: "avatar_decoration_description_dropped", Kind: "avatar_decoration",
			Message: fmt.Sprintf("%d avatar decoration description(s) were not imported; Community decorations have no description", descriptions)})
	}
	for _, s := range i.bundle.AvatarDecorationSelections {
		if i.ids["account"][s.AccountID] == 0 {
			i.report.Issues = append(i.report.Issues, Issue{Severity: "info", Code: "avatar_decoration_account_missing", Kind: "avatar_decoration_selection", SourceID: s.AccountID,
				Message: "decorations of an account that was not imported are skipped"})
			continue
		}
		list := models.AvatarDecorationSelections{}
		seen := map[uint64]bool{}
		for _, p := range s.Decorations {
			local := i.ids["avatar_decoration"][p.ID]
			if local == 0 {
				i.report.Issues = append(i.report.Issues, Issue{Severity: "info", Code: "avatar_decoration_unknown", Kind: "avatar_decoration_selection", SourceID: s.AccountID,
					Message: "a selected avatar decoration is not among the imported definitions and was skipped"})
				continue
			}
			if seen[local] || len(list) >= models.MaxAvatarDecorations {
				continue
			}
			seen[local] = true
			list = append(list, models.AvatarDecorationSelection{ID: strconv.FormatUint(local, 10), Angle: p.Angle, FlipH: p.FlipH, OffsetX: p.OffsetX, OffsetY: p.OffsetY}.Normalized())
		}
		if len(list) == 0 {
			continue
		}
		digest, err := digestValue(list)
		if err != nil {
			return err
		}
		accountID := i.ids["account"][s.AccountID]
		if _, err = i.apply("avatar_decoration_selection", s.AccountID+"\n"+digest, list, func(tx *gorm.DB) (uint64, error) {
			first, _ := strconv.ParseUint(list[0].ID, 10, 64)
			err := tx.Model(&models.Account{}).Where("id = ?", accountID).Updates(map[string]any{"avatar_decorations": list, "avatar_decoration_id": first}).Error
			return accountID, err
		}); err != nil {
			return err
		}
	}
	return nil
}

// decorationImageURL is the target URL of a decoration's imported image:
// the local media path, else the kept object-storage URL. "" when the image
// was not imported.
func (i *importer) decorationImageURL(d AvatarDecoration) (string, error) {
	if d.MediaID == "" {
		return "", nil
	}
	id := i.ids["media"][d.MediaID]
	if id == 0 {
		return "", nil
	}
	var media models.MediaAttachment
	if err := i.tx.First(&media, id).Error; err != nil {
		return "", err
	}
	if path, err := mediaAliasTarget(&media); err == nil {
		return strings.TrimRight(i.options.TargetBaseURL, "/") + path, nil
	}
	if safeHTTPURL(media.RemoteUrl) {
		return media.RemoteUrl, nil
	}
	return "", nil
}
