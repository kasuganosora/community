package datatransfer

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func cloneBundle(t *testing.T, b *Bundle) *Bundle {
	raw, err := json.Marshal(b)
	require.NoError(t, err)
	var out Bundle
	require.NoError(t, json.Unmarshal(raw, &out))
	return &out
}

func decorationFixture(t *testing.T) *Bundle {
	b := streamFixture(t, 3, 4)
	for _, id := range []string{"d1", "d2"} {
		b.Media = append(b.Media, Media{ID: "avatar_decoration:" + id, Name: id + ".png", ContentType: "image/png", RemoteURL: "https://files.site.example/" + id + ".png", StorageReference: true})
	}
	b.AvatarDecorations = []AvatarDecoration{
		{ID: "d1", Name: "Cat ears", Description: "meow", URL: "https://files.site.example/d1.png", MediaID: "avatar_decoration:d1"},
		{ID: "d2", Name: "Halo", URL: "https://files.site.example/d2.png", MediaID: "avatar_decoration:d2"},
	}
	b.AvatarDecorationSelections = []AvatarDecorationSelection{
		{AccountID: "u0", Decorations: []AvatarDecorationPlace{{ID: "d1", Angle: 0.125, FlipH: true, OffsetX: 0.05, OffsetY: -0.1}, {ID: "gone"}, {ID: "d2"}}},
		{AccountID: "u1", Decorations: []AvatarDecorationPlace{{ID: "d2", Angle: -0.25}}},
	}
	return b
}

func decorationState(t *testing.T, conn *gorm.DB) (map[string]uint64, map[string]models.AvatarDecorationSelections) {
	var defs []models.AvatarDecoration
	require.NoError(t, conn.Order("id").Find(&defs).Error)
	byName := map[string]uint64{}
	for _, d := range defs {
		require.True(t, d.Enabled, "imported decorations are available to everyone")
		byName[d.Name] = d.ID
	}
	var accounts []models.Account
	require.NoError(t, conn.Where("username IN ?", []string{"u0", "u1", "u2"}).Find(&accounts).Error)
	sel := map[string]models.AvatarDecorationSelections{}
	for _, a := range accounts {
		sel[a.Username] = a.AvatarDecorations
	}
	return byName, sel
}

func checkDecorations(t *testing.T, conn *gorm.DB) {
	defs, sel := decorationState(t, conn)
	require.Len(t, defs, 2)
	d1, d2 := strconv.FormatUint(defs["Cat ears"], 10), strconv.FormatUint(defs["Halo"], 10)
	require.Equal(t, models.AvatarDecorationSelections{{ID: d1, Angle: 0.125, FlipH: true, OffsetX: 0.05, OffsetY: -0.1}, {ID: d2}}, sel["u0"])
	require.Equal(t, models.AvatarDecorationSelections{{ID: d2, Angle: -0.25}}, sel["u1"])
	require.Empty(t, sel["u2"])
	var img models.AvatarDecoration
	require.NoError(t, conn.First(&img, defs["Cat ears"]).Error)
	require.Equal(t, "https://files.site.example/d1.png", img.ImageURL)
	var a models.Account
	require.NoError(t, conn.Where("username = ?", "u0").First(&a).Error)
	require.Equal(t, defs["Cat ears"], a.AvatarDecorationID)
}

func hasIssue(r *Report, code string) bool {
	for _, is := range r.Issues {
		if is.Code == code {
			return true
		}
	}
	return false
}

func TestImportAvatarDecorationsWholeBundle(t *testing.T) {
	base := decorationFixture(t)
	b := cloneBundle(t, base)
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	conn := importerDB(t)
	report, err := Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err, "%+v", report.Issues)
	require.True(t, hasIssue(report, "avatar_decoration_unknown"), "unknown decoration is reported, not fatal")
	require.True(t, hasIssue(report, "avatar_decoration_description_dropped"))
	require.Equal(t, 2, report.Created["avatar_decoration"])
	checkDecorations(t, conn)

	// Re-run: idempotent.
	again, err := Import(context.Background(), conn, cloneBundle(t, base), o, nil)
	require.NoError(t, err)
	require.Zero(t, again.Created["avatar_decoration"])
	require.Zero(t, again.Created["avatar_decoration_selection"])
	checkDecorations(t, conn)

	// Final sync with a changed selection and a renamed decoration applies both.
	b = cloneBundle(t, base)
	b.AvatarDecorations[1].Name = "Golden halo"
	b.AvatarDecorationSelections[1].Decorations[0].Angle = 0.5
	_, err = Import(context.Background(), conn, b, o, nil)
	require.NoError(t, err)
	defs, sel := decorationState(t, conn)
	require.Len(t, defs, 2)
	require.Equal(t, models.AvatarDecorationSelections{{ID: strconv.FormatUint(defs["Golden halo"], 10), Angle: 0.5}}, sel["u1"])
}

func TestImportAvatarDecorationsStreamed(t *testing.T) {
	b := decorationFixture(t)
	o := Options{Scope: "site", TargetBaseURL: "https://site.example", StorageDir: t.TempDir()}
	conn := importerDB(t)
	sp := spoolBundle(t, b)
	preview, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 1})
	require.NoError(t, err)
	require.True(t, preview.CanExecute, "%+v", preview.Issues)
	report, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 1, Apply: true})
	require.NoError(t, err, "%+v", report.Issues)
	require.True(t, report.CanExecute)
	require.Equal(t, 2, report.Counts["avatar_decoration"])
	checkDecorations(t, conn)
	again, err := ImportSpool(context.Background(), conn, sp, o, StreamOptions{ChunkSize: 1, Apply: true})
	require.NoError(t, err)
	require.Zero(t, again.Created["avatar_decoration"])
	checkDecorations(t, conn)
}
