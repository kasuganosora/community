package datatransfer

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/kasuganosora/community/db/dialect"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// ColumnTooNarrowCode is the blocking issue raised when a staged value is
// longer than the target column that will store it. Without it, validation
// passes and the real import dies mid-way on "value too long" (the maid.lat
// rehearsal: 257 remote emoji shortcodes > varchar(36)).
const ColumnTooNarrowCode = "column_too_narrow"

// maxWidthSamples bounds the sample source IDs kept per column.
const maxWidthSamples = 5

// columnTarget names one target column the importer writes. values emits
// (source id, value) for every staged record, exactly as the importer will
// store it.
type columnTarget struct {
	model  any
	column string
	values func(b *Bundle, emit func(id, value string))
}

// importColumnTargets is the declared map from staged fields to target
// columns. Widths are not declared here: they are read from the live schema,
// so the check follows whatever migrations the target has applied. Text and
// JSON columns report no width and are skipped.
var importColumnTargets = []columnTarget{
	{&models.Emoji{}, "shortcode", func(b *Bundle, emit func(string, string)) {
		for _, e := range b.Emojis {
			emit(e.ID, e.Shortcode)
		}
	}},
	{&models.Emoji{}, "domain", func(b *Bundle, emit func(string, string)) {
		for _, e := range b.Emojis {
			emit(e.ID, e.Domain)
		}
	}},
	{&models.Emoji{}, "category", func(b *Bundle, emit func(string, string)) {
		for _, e := range b.Emojis {
			emit(e.ID, e.Category)
		}
	}},
	{&models.Emoji{}, "url", func(b *Bundle, emit func(string, string)) {
		for _, e := range b.Emojis {
			if e.MediaID == "" {
				emit(e.ID, e.URL)
			}
		}
	}},
	{&models.Account{}, "username", func(b *Bundle, emit func(string, string)) {
		for _, a := range b.Accounts {
			emit(a.ID, a.Username)
		}
	}},
	{&models.User{}, "username", func(b *Bundle, emit func(string, string)) {
		for _, a := range b.Accounts {
			if a.Local && !a.System && b.Scope == "site" {
				emit(a.ID, a.Username)
			}
		}
	}},
	{&models.Account{}, "display_name", func(b *Bundle, emit func(string, string)) {
		for _, a := range b.Accounts {
			emit(a.ID, a.DisplayName)
		}
	}},
	{&models.Account{}, "domain", func(b *Bundle, emit func(string, string)) {
		for _, a := range b.Accounts {
			if !a.Local {
				emit(a.ID, importedAccountDomain(a))
			}
		}
	}},
	{&models.Account{}, "acct", func(b *Bundle, emit func(string, string)) {
		for _, a := range b.Accounts {
			if a.Local {
				emit(a.ID, a.Username+"@__LOCAL__")
			} else {
				emit(a.ID, a.Username+"@"+importedAccountDomain(a))
			}
		}
	}},
	{&models.Account{}, "origin_id", func(b *Bundle, emit func(string, string)) {
		for _, a := range b.Accounts {
			if !a.Local {
				emit(a.ID, a.ActorURI)
			}
		}
	}},
	{&models.Account{}, "local_actor_uri", func(b *Bundle, emit func(string, string)) {
		for _, a := range b.Accounts {
			if a.Local {
				emit(a.ID, a.ActorURI)
			}
		}
	}},
	{&models.Account{}, "avatar", func(b *Bundle, emit func(string, string)) {
		for _, a := range b.Accounts {
			if a.AvatarID == "" {
				emit(a.ID, a.AvatarURL)
			}
		}
	}},
	{&models.Account{}, "header", func(b *Bundle, emit func(string, string)) {
		for _, a := range b.Accounts {
			if a.HeaderID == "" {
				emit(a.ID, a.HeaderURL)
			}
		}
	}},
	{&models.UserSecretData{}, "password_scheme", func(b *Bundle, emit func(string, string)) {
		for _, c := range b.Credentials {
			emit(c.AccountID, c.PasswordScheme)
		}
	}},
	{&models.UserSecretData{}, "password", func(b *Bundle, emit func(string, string)) {
		for _, c := range b.Credentials {
			emit(c.AccountID, c.PasswordHash)
		}
	}},
	{&models.UserSecretData{}, "salt", func(b *Bundle, emit func(string, string)) {
		for _, c := range b.Credentials {
			emit(c.AccountID, c.PasswordSalt)
		}
	}},
	{&models.AccountKey{}, "key_id", func(b *Bundle, emit func(string, string)) {
		for _, k := range b.Keys {
			emit(k.AccountID, k.KeyID)
		}
	}},
	{&models.AccountFederationKey{}, "algorithm", func(b *Bundle, emit func(string, string)) {
		for _, k := range b.Keys {
			emit(k.AccountID, k.Algorithm)
		}
	}},
	{&models.Status{}, "origin_uri", func(b *Bundle, emit func(string, string)) {
		forRemotePosts(b, func(p Post) { emit(p.ID, p.URI) })
	}},
	{&models.Status{}, "origin_url", func(b *Bundle, emit func(string, string)) {
		forRemotePosts(b, func(p Post) { emit(p.ID, p.URL) })
	}},
	{&models.Status{}, "local_object_uri", func(b *Bundle, emit func(string, string)) {
		local := localAccountIDs(b)
		for _, p := range b.Posts {
			if local[p.AuthorID] {
				emit(p.ID, p.URI)
			}
		}
	}},
	{&models.Status{}, "language", func(b *Bundle, emit func(string, string)) {
		for _, p := range b.Posts {
			emit(p.ID, p.Language)
		}
	}},
	{&models.MediaAttachment{}, "remote_url", func(b *Bundle, emit func(string, string)) {
		for _, m := range b.Media {
			emit(m.ID, m.RemoteURL)
		}
	}},
	{&models.MediaAttachment{}, "content_type", func(b *Bundle, emit func(string, string)) {
		for _, m := range b.Media {
			emit(m.ID, m.ContentType)
		}
	}},
	{&models.MediaAttachment{}, "blurhash", func(b *Bundle, emit func(string, string)) {
		for _, m := range b.Media {
			emit(m.ID, m.Blurhash)
		}
	}},
	{&models.List{}, "title", func(b *Bundle, emit func(string, string)) {
		for _, l := range b.Lists {
			emit(l.ID, l.Title)
		}
	}},
	{&models.StatusReaction{}, "name", func(b *Bundle, emit func(string, string)) {
		for _, r := range b.Reactions {
			emit(r.ID, r.Emoji)
		}
	}},
}

func importedAccountDomain(a Account) string {
	if a.Domain != "" {
		return a.Domain
	}
	if u, err := url.Parse(a.ActorURI); err == nil {
		return u.Host
	}
	return ""
}

func localAccountIDs(b *Bundle) map[string]bool {
	out := map[string]bool{}
	for _, a := range b.Accounts {
		if a.Local {
			out[a.ID] = true
		}
	}
	return out
}

// forRemotePosts visits posts the importer stores with origin_uri/url: those
// by accounts in this bundle that are not local. Posts whose author is only a
// seeded reference are checked conservatively as remote.
func forRemotePosts(b *Bundle, f func(Post)) {
	local := localAccountIDs(b)
	for _, p := range b.Posts {
		if !local[p.AuthorID] {
			f(p)
		}
	}
}

// ColumnWidths reports the maximum character length of each varchar/char
// column of table, keyed by column name. Columns without a declared width
// (text, json, numbers) are absent.
type ColumnWidths map[string]int64

// WidthCache memoizes schema introspection for one import run.
type WidthCache struct {
	tables map[string]ColumnWidths
	// Strict also checks engines that do not enforce declared widths
	// (SQLite). Off by default: an old SQLite schema keeps its narrow DDL
	// forever (widening there is a no-op) yet stores any length.
	Strict bool
}

func NewWidthCache() *WidthCache { return &WidthCache{tables: map[string]ColumnWidths{}} }

func (c *WidthCache) enforced(conn *gorm.DB) bool {
	if dialect.Name(conn) != dialect.SQLite {
		return true
	}
	return c != nil && c.Strict
}

func tableName(conn *gorm.DB, model any) (string, error) {
	stmt := &gorm.Statement{DB: conn}
	if err := stmt.Parse(model); err != nil {
		return "", err
	}
	return stmt.Schema.Table, nil
}

func (c *WidthCache) widths(conn *gorm.DB, table string) (ColumnWidths, error) {
	if c != nil && c.tables == nil {
		c.tables = map[string]ColumnWidths{}
	}
	if c != nil {
		if w, ok := c.tables[table]; ok {
			return w, nil
		}
	}
	w := ColumnWidths{}
	if conn.Migrator().HasTable(table) {
		types, err := conn.Migrator().ColumnTypes(table)
		if err != nil {
			return nil, err
		}
		for _, ct := range types {
			if !isCharacterColumn(ct.DatabaseTypeName()) {
				continue
			}
			if n, ok := ct.Length(); ok && n > 0 && n < 1<<20 {
				w[strings.ToLower(ct.Name())] = n
			}
		}
	}
	if c != nil {
		c.tables[table] = w
	}
	return w, nil
}

func isCharacterColumn(t string) bool {
	t = strings.ToLower(t)
	return strings.Contains(t, "char") && !strings.Contains(t, "text")
}

type widthViolation struct {
	table, column string
	limit         int64
	count, max    int
	samples       []string
	seen          map[string]bool
}

// checkColumnWidths compares every staged value with the width of the target
// column it will be written to and adds one blocking issue per column.
func checkColumnWidths(conn *gorm.DB, b *Bundle, cache *WidthCache, r *Report) error {
	if !cache.enforced(conn) {
		return nil
	}
	var found []*widthViolation
	for _, target := range importColumnTargets {
		table, err := tableName(conn, target.model)
		if err != nil {
			return err
		}
		widths, err := cache.widths(conn, table)
		if err != nil {
			return err
		}
		limit, ok := widths[target.column]
		if !ok {
			continue
		}
		v := &widthViolation{table: table, column: target.column, limit: limit, seen: map[string]bool{}}
		target.values(b, func(id, value string) {
			n := utf8.RuneCountInString(value)
			if int64(n) <= limit || v.seen[id] {
				return
			}
			v.seen[id] = true
			v.count++
			if n > v.max {
				v.max = n
			}
			if len(v.samples) < maxWidthSamples {
				v.samples = append(v.samples, id)
			}
		})
		if v.count > 0 {
			found = append(found, v)
		}
	}
	for _, v := range found {
		is := widthIssue(v.table, v.column, v.limit, v.count, v.max, v.samples)
		for id := range v.seen {
			is.violators = append(is.violators, id)
		}
		r.Issues = append(r.Issues, is)
		r.CanExecute = false
	}
	return nil
}

func widthIssue(table, column string, limit int64, count, max int, samples []string) Issue {
	return Issue{Severity: "error", Code: ColumnTooNarrowCode, Kind: table + "." + column, SourceID: firstOr(samples),
		Message: fmt.Sprintf("%d staged value(s) exceed %s.%s (width %d, longest %d); sample source ids: %s; run the target schema migration (community migrate / community-transfer applies it) and re-run validate",
			count, table, column, limit, max, strings.Join(samples, ",")),
		Count: count, MaxLength: max, Limit: limit, SampleIDs: samples}
}

func firstOr(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// widthTally accumulates column_too_narrow issues across streamed chunks.
type widthTally struct {
	order  []string
	issues map[string]*Issue
	ids    map[string]map[string]bool
}

func (t *widthTally) add(is Issue) {
	if t.issues == nil {
		t.issues, t.ids = map[string]*Issue{}, map[string]map[string]bool{}
	}
	cur, ok := t.issues[is.Kind]
	if !ok {
		cp := is
		cp.violators = nil
		cp.Count = 0
		cp.SampleIDs = nil
		t.issues[is.Kind], t.ids[is.Kind] = &cp, map[string]bool{}
		t.order = append(t.order, is.Kind)
		cur = &cp
	}
	ids := t.ids[is.Kind]
	for _, id := range is.violators {
		if ids[id] {
			continue
		}
		ids[id] = true
		cur.Count++
		if len(cur.SampleIDs) < maxWidthSamples {
			cur.SampleIDs = append(cur.SampleIDs, id)
		}
	}
	if is.MaxLength > cur.MaxLength {
		cur.MaxLength = is.MaxLength
	}
	sort.Strings(cur.SampleIDs)
	table, column, _ := strings.Cut(cur.Kind, ".")
	*cur = widthIssue(table, column, cur.Limit, cur.Count, cur.MaxLength, cur.SampleIDs)
}

// list returns the merged issues in first-seen order.
func (t *widthTally) list() []Issue {
	out := make([]Issue, 0, len(t.order))
	for _, k := range t.order {
		out = append(out, *t.issues[k])
	}
	return out
}
