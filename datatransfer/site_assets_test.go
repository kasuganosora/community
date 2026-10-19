package datatransfer

import (
	"context"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
)

// Ownerless Misskey drive files used by meta (icon, banner, ...) are imported
// as instance assets owned by the instance actor and wired into the target's
// branding settings; a value already set on the target is kept.
func TestImportMisskeyMetaAssets(t *testing.T) {
	b := streamFixture(t, 3, 4)
	base := "https://site.example"
	b.Accounts = append(b.Accounts, Account{ID: "ia", Username: "instance.actor", Local: true, System: true, SystemRole: "instance_actor", ActorURI: base + "/users/ia"})
	k := b.Keys[0]
	k.AccountID, k.KeyID = "ia", base+"/users/ia#main-key"
	b.Keys = append(b.Keys, k)
	b.Media = append(b.Media,
		Media{ID: "icon", Name: "icon.png", ContentType: "image/png", RemoteURL: "https://files.site.example/icon.png", StorageReference: true, SiteAssets: []string{"iconUrl", "app192IconUrl"}},
		Media{ID: "banner", Name: "banner.png", ContentType: "image/png", RemoteURL: "https://files.site.example/banner.png", StorageReference: true, SiteAssets: []string{"bannerUrl"}})
	o := Options{Scope: "site", TargetBaseURL: base, StorageDir: t.TempDir()}
	conn := importerDB(t)
	require.NoError(t, conn.AutoMigrate(&models.SystemSetting{}, &models.Relay{}))
	require.NoError(t, conn.Create(&models.SystemSetting{Key: systemsettings.KeyBrandBannerURL, Val: "https://elsewhere.example/b.png"}).Error)
	sp := spoolBundle(t, b)
	var report *Report
	for range 2 {
		var err error
		report, err = ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 2, Apply: true})
		require.NoError(t, err, "%+v", report)
	}
	get := func(key string) any {
		var s models.SystemSetting
		require.NoError(t, conn.Where("key = ?", key).First(&s).Error)
		return s.Val
	}
	require.Equal(t, "https://files.site.example/icon.png", get(systemsettings.KeyBrandIconURL))
	require.Equal(t, "https://files.site.example/icon.png", get(systemsettings.KeyBrandApp192IconURL))
	require.Equal(t, "https://elsewhere.example/b.png", get(systemsettings.KeyBrandBannerURL), "target value kept")
	var ia models.Account
	require.NoError(t, conn.Where("local_actor_uri = ?", base+"/users/ia").First(&ia).Error)
	var owned int64
	require.NoError(t, conn.Model(&models.MediaAttachment{}).Where("account_id = ? AND remote_url LIKE ?", ia.Id, "https://files.site.example/%").Count(&owned).Error)
	require.EqualValues(t, 2, owned, "assets belong to the instance actor")
}
