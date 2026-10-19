package datatransfer

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// misskeyAssetSettings maps Misskey meta image columns to the branding
// settings c.hime.at serves (same mapping as tootctl import-misskey-meta).
var misskeyAssetSettings = map[string]string{
	"iconUrl":             systemsettings.KeyBrandIconURL,
	"app192IconUrl":       systemsettings.KeyBrandApp192IconURL,
	"app512IconUrl":       systemsettings.KeyBrandApp512IconURL,
	"bannerUrl":           systemsettings.KeyBrandBannerURL,
	"backgroundImageUrl":  systemsettings.KeyBrandBackgroundImageURL,
	"infoImageUrl":        systemsettings.KeyBrandInfoImageURL,
	"serverErrorImageUrl": systemsettings.KeyBrandServerErrorImageURL,
	"notFoundImageUrl":    systemsettings.KeyBrandNotFoundImageURL,
}

// importSiteAssets points the target's branding settings at imported
// instance asset files (Misskey ownerless drive files used by meta). It only
// writes the database being imported, and only a setting that is unset or
// still holds the source URL (e.g. copied by import-misskey-meta); a value
// set on the target is kept and reported.
func (i *importer) importSiteAssets() error {
	if i.personal() {
		return nil
	}
	for _, m := range i.bundle.Media {
		if len(m.SiteAssets) == 0 {
			continue
		}
		id := i.ids["media"][m.ID]
		if id == 0 {
			continue
		}
		var target models.MediaAttachment
		if err := i.tx.First(&target, id).Error; err != nil {
			return err
		}
		value := target.RemoteUrl
		if path, err := mediaAliasTarget(&target); err == nil {
			value = strings.TrimRight(i.options.TargetBaseURL, "/") + path
		}
		if value == "" {
			continue
		}
		for _, column := range m.SiteAssets {
			key := misskeyAssetSettings[column]
			if key == "" {
				continue
			}
			var existing models.SystemSetting
			err := i.tx.Where(clause.Eq{Column: clause.Column{Name: "key"}, Value: key}).First(&existing).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				if err := i.tx.Create(&models.SystemSetting{Key: key, Val: value}).Error; err != nil {
					return err
				}
				i.report.Created["site_asset"]++
			case err != nil:
				return err
			default:
				current, _ := existing.Val.(string)
				switch {
				case current == value:
				case current == "" || (m.OriginalURL != "" && current == m.OriginalURL) || current == m.RemoteURL:
					existing.Val = value
					if err := i.tx.Save(&existing).Error; err != nil {
						return err
					}
					i.report.Created["site_asset"]++
				default:
					i.report.Issues = append(i.report.Issues, Issue{Severity: "info", Code: "site_asset_kept", Kind: "media", SourceID: m.ID,
						Message: fmt.Sprintf("%s is already set on the target; the imported meta.%s file was not applied", key, column)})
				}
			}
		}
	}
	return nil
}
