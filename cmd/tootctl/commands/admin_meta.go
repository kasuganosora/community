package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/urfave/cli/v2"
	"gorm.io/gorm"
)

// AdminCommand implements tootctl admin.
func AdminCommand() *cli.Command {
	return &cli.Command{
		Name:  "admin",
		Usage: "Instance administration helpers",
		Subcommands: []*cli.Command{
			{
				Name:  "import-misskey-meta",
				Usage: "Import moderation, branding, captcha, SMTP and object-storage settings from a Misskey PostgreSQL database",
				Description: `Reads Misskey's meta row and imports the admin "moderation" settings:
disableRegistration, emailRequiredForSignup, ugcVisibilityForVisitor,
preservedUsernames, sensitiveWords, prohibitedWords,
prohibitedWordsForNameOfUser, hiddenTags, blockedHosts, silencedHosts,
mediaSilencedHosts and serverRules (added to the rules list when missing),
plus manually suspended instances from Misskey's instance table.
Branding fields are imported only where this server has a matching setting
(name, description, maintainerEmail ...). Turnstile (and hCaptcha / reCAPTCHA),
SMTP columns and object storage are written to system_settings (config.captcha.*,
config.email.*, config.storage.*) when those keys are not already saved.
Columns missing on either side are skipped, so older or newer Misskey versions
work. Lists replace the current values; run with --dry-run first.`,
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "dsn", EnvVars: []string{"MISSKEY_DSN"}, Usage: "Misskey PostgreSQL DSN/URL (or $MISSKEY_DSN)"},
					&cli.BoolFlag{Name: "dry-run", Usage: "only report what would be imported"},
					&cli.BoolFlag{Name: "skip-branding", Usage: "do not import branding fields"},
					&cli.BoolFlag{Name: "skip-registration", Usage: "do not import disableRegistration / emailRequiredForSignup"},
					&cli.BoolFlag{Name: "skip-instances", Usage: "do not import suspended instances"},
				},
				Action: func(c *cli.Context) error {
					dsn := c.String("dsn")
					if dsn == "" {
						return cli.Exit("--dsn (or MISSKEY_DSN) is required", 1)
					}
					src, closeSrc, err := misskeySource(dsn)
					if err != nil {
						return cli.Exit("could not connect to the Misskey database", 1)
					}
					defer closeSrc()
					opts := MisskeyMetaOptions{
						DryRun:           c.Bool("dry-run"),
						SkipBranding:     c.Bool("skip-branding"),
						SkipRegistration: c.Bool("skip-registration"),
						SkipInstances:    c.Bool("skip-instances"),
					}
					return ImportMisskeyMeta(src.WithContext(c.Context), db.GetDB().WithContext(c.Context), c.App.Writer, opts)
				},
			},
		},
	}
}

// MisskeyMetaOptions selects what ImportMisskeyMeta imports.
type MisskeyMetaOptions struct {
	DryRun, SkipBranding, SkipRegistration, SkipInstances bool
}

// misskeyListSettings maps Misskey meta array columns to moderation keys.
var misskeyListSettings = []struct{ column, key string }{
	{"preservedUsernames", moderation.KeyPreservedUsernames},
	{"sensitiveWords", moderation.KeySensitiveWords},
	{"prohibitedWords", moderation.KeyProhibitedWords},
	{"prohibitedWordsForNameOfUser", moderation.KeyProhibitedWordsForNameOfUser},
	{"hiddenTags", moderation.KeyHiddenTags},
	{"blockedHosts", moderation.KeyBlockedHosts},
	{"silencedHosts", moderation.KeySilencedHosts},
	{"mediaSilencedHosts", moderation.KeyMediaSilencedHosts},
}

// misskeyBrandingSettings maps Misskey meta text columns to settings that
// exist in this schema. Add entries here when new branding settings land.
var misskeyBrandingSettings = map[string]string{
	"name":                systemsettings.KeyTitle,
	"description":         systemsettings.KeyDescription,
	"maintainerEmail":     systemsettings.KeySiteContactEmail,
	"iconUrl":             systemsettings.KeyBrandIconURL,
	"app192IconUrl":       systemsettings.KeyBrandApp192IconURL,
	"app512IconUrl":       systemsettings.KeyBrandApp512IconURL,
	"bannerUrl":           systemsettings.KeyBrandBannerURL,
	"backgroundImageUrl":  systemsettings.KeyBrandBackgroundImageURL,
	"notFoundImageUrl":    systemsettings.KeyBrandNotFoundImageURL,
	"infoImageUrl":        systemsettings.KeyBrandInfoImageURL,
	"serverErrorImageUrl": systemsettings.KeyBrandServerErrorImageURL,
	"themeColor":          systemsettings.KeyBrandThemeColor,
	"defaultLightTheme":   systemsettings.KeyBrandDefaultLightTheme,
	"defaultDarkTheme":    systemsettings.KeyBrandDefaultDarkTheme,
	"repositoryUrl":       systemsettings.KeyBrandRepositoryURL,
	"feedbackUrl":         systemsettings.KeyBrandFeedbackURL,
}

// misskeyColumns returns the columns of a Misskey table and whether each is
// an array.
func misskeyColumns(src *gorm.DB, table string) (map[string]bool, error) {
	var rows []struct {
		ColumnName string
		DataType   string
	}
	if err := src.Raw(`SELECT column_name, data_type FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ?`, table).Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[r.ColumnName] = r.DataType == "ARRAY"
	}
	return out, nil
}

func quoteIdent(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// loadMisskeyMeta reads the wanted meta columns as text; arrays are read
// as JSON.
func loadMisskeyMeta(src *gorm.DB) (map[string]string, map[string][]string, error) {
	cols, err := misskeyColumns(src, "meta")
	if err != nil {
		return nil, nil, err
	}
	if len(cols) == 0 {
		return nil, nil, fmt.Errorf("misskey meta table not found")
	}
	want := []string{"disableRegistration", "emailRequiredForSignup", "ugcVisibilityForVisitor", "serverRules"}
	want = append(want, misskeyServiceColumns()...)
	for _, l := range misskeyListSettings {
		want = append(want, l.column)
	}
	for column := range misskeyBrandingSettings {
		want = append(want, column)
	}
	sort.Strings(want)
	var selects, names []string
	for _, column := range want {
		isArray, ok := cols[column]
		if !ok {
			continue
		}
		expr := quoteIdent(column) + "::text"
		if isArray {
			expr = "array_to_json(" + quoteIdent(column) + ")::text"
		}
		selects = append(selects, "COALESCE("+expr+", '') AS "+quoteIdent(column))
		names = append(names, column)
	}
	scalars, lists := map[string]string{}, map[string][]string{}
	if len(selects) == 0 {
		return scalars, lists, nil
	}
	row := map[string]any{}
	if err := src.Raw(`SELECT ` + strings.Join(selects, ", ") + ` FROM meta LIMIT 1`).Take(&row).Error; err != nil {
		return nil, nil, err
	}
	for _, column := range names {
		v := fmt.Sprint(row[column])
		if b, ok := row[column].([]byte); ok {
			v = string(b)
		}
		if cols[column] {
			var l []string
			if v != "" {
				if err := json.Unmarshal([]byte(v), &l); err != nil {
					return nil, nil, fmt.Errorf("meta.%s: %w", column, err)
				}
			}
			lists[column] = l
		} else {
			scalars[column] = v
		}
	}
	return scalars, lists, nil
}

// ImportMisskeyMeta imports instance settings from a Misskey database (src)
// into this server (dst), writing a line per change to out. It is shared by
// `tootctl admin import-misskey-meta` and the setup wizard's migration.
func ImportMisskeyMeta(src, dst *gorm.DB, out io.Writer, opts MisskeyMetaOptions) error {
	scalars, lists, err := loadMisskeyMeta(src)
	if err != nil {
		return err
	}
	prefix := ""
	if opts.DryRun {
		prefix = "[dry run] "
	}
	upd := moderation.Update{}
	set := func(column string, target **[]string) {
		if l, ok := lists[column]; ok {
			*target = &l
		}
	}
	set("preservedUsernames", &upd.PreservedUsernames)
	set("sensitiveWords", &upd.SensitiveWords)
	set("prohibitedWords", &upd.ProhibitedWords)
	set("prohibitedWordsForNameOfUser", &upd.ProhibitedWordsForNameOfUser)
	set("hiddenTags", &upd.HiddenTags)
	set("blockedHosts", &upd.BlockedHosts)
	set("silencedHosts", &upd.SilencedHosts)
	set("mediaSilencedHosts", &upd.MediaSilencedHosts)
	if v, ok := scalars["ugcVisibilityForVisitor"]; ok && v != "" {
		v = moderation.NormalizeUGC(v)
		upd.UGCVisibilityForVisitor = &v
	}
	changes, err := upd.Changes()
	if err != nil {
		return err
	}
	if !opts.SkipRegistration {
		if v, ok := scalars["disableRegistration"]; ok && v != "" {
			changes[systemsettings.KeyIsEnableRegistration] = v != "true"
		}
		if v, ok := scalars["emailRequiredForSignup"]; ok && v != "" {
			changes[systemsettings.KeyRegistrationCheckEmail] = v == "true"
		}
	}
	if !opts.SkipBranding {
		for column, key := range misskeyBrandingSettings {
			if v, ok := scalars[column]; ok && strings.TrimSpace(v) != "" {
				changes[key] = v
			}
		}
	}
	// Captcha, SMTP and object storage are filled only when that key is not
	// already saved, so a re-run does not overwrite a later admin edit.
	for key, value := range WithoutStored(MisskeyServiceSettings(scalars)) {
		changes[key] = value
	}
	keys := make([]string, 0, len(changes))
	for k := range changes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		switch v := changes[k].(type) {
		case []string:
			_, _ = fmt.Fprintf(out, "%s%s: %d entries\n", prefix, k, len(v))
		default:
			_, _ = fmt.Fprintf(out, "%s%s = %s\n", prefix, k, settingLogValue(k, v))
		}
	}
	if !opts.DryRun {
		if err := moderation.SaveRaw(changes); err != nil {
			return err
		}
	}

	added, err := importMisskeyServerRules(dst, lists["serverRules"], opts.DryRun)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "%sserver rules: %d in Misskey, %d added\n", prefix, len(lists["serverRules"]), added)

	if !opts.SkipInstances {
		n, err := importMisskeySuspendedInstances(src, dst, opts.DryRun)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(out, "%ssuspended instances: %d\n", prefix, n)
	}
	return nil
}

func importMisskeyServerRules(dst *gorm.DB, rules []string, dryRun bool) (int, error) {
	var existing []models.Rule
	if err := dst.Find(&existing).Error; err != nil {
		return 0, err
	}
	have := map[string]bool{}
	maxPriority := uint(0)
	for _, r := range existing {
		have[strings.TrimSpace(r.Text)] = true
		if r.Priority > maxPriority {
			maxPriority = r.Priority
		}
	}
	added := 0
	for _, text := range rules {
		text = strings.TrimSpace(text)
		if text == "" || have[text] {
			continue
		}
		if len([]rune(text)) > 255 {
			text = string([]rune(text)[:255])
		}
		have[text] = true
		added++
		maxPriority++
		if dryRun {
			continue
		}
		if err := dst.Create(&models.Rule{Text: text, Priority: maxPriority}).Error; err != nil {
			return added, err
		}
	}
	return added, nil
}

func importMisskeySuspendedInstances(src, dst *gorm.DB, dryRun bool) (int, error) {
	cols, err := misskeyColumns(src, "instance")
	if err != nil {
		return 0, err
	}
	var where string
	switch {
	case hasColumn(cols, "suspensionState"):
		where = `"suspensionState"::text = 'manuallySuspended'`
	case hasColumn(cols, "isSuspended"):
		where = `"isSuspended" = true`
	default:
		return 0, nil
	}
	var hosts []string
	if err := src.Raw(`SELECT host FROM instance WHERE ` + where).Scan(&hosts).Error; err != nil {
		return 0, err
	}
	if dryRun {
		return len(hosts), nil
	}
	for _, h := range hosts {
		if err := moderation.SetInstanceSuspended(dst, h, true); err != nil {
			return 0, err
		}
	}
	return len(hosts), nil
}

func hasColumn(cols map[string]bool, name string) bool {
	_, ok := cols[name]
	return ok
}

func settingLogValue(key string, value any) string {
	name := strings.ToLower(key)
	if strings.Contains(name, "secret") || strings.Contains(name, "password") {
		if strings.TrimSpace(fmt.Sprint(value)) == "" {
			return "(empty)"
		}
		return "(set)"
	}
	return fmt.Sprint(value)
}
