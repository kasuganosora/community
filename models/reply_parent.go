package models

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

// StatusPropReplyToURI keeps an ActivityPub inReplyTo address when the parent
// was not stored yet. Later reads resolve it locally; they never fetch it.
const StatusPropReplyToURI = "reply_to_uri"

var statusURLInText = regexp.MustCompile(`https?://[^\s"'<>]+`)

// ReplyToURI returns a previously stored inReplyTo address, if any.
func ReplyToURI(status *Status) string {
	if status == nil || status.Properties == nil {
		return ""
	}
	uri, _ := status.Properties[StatusPropReplyToURI].(string)
	return strings.TrimSpace(uri)
}

// SetReplyToURI records or clears the unresolved inReplyTo address.
func SetReplyToURI(status *Status, uri string) {
	if status == nil {
		return
	}
	uri = strings.TrimSpace(uri)
	if uri == "" {
		if status.Properties != nil {
			delete(status.Properties, StatusPropReplyToURI)
		}
		return
	}
	if status.Properties == nil {
		status.Properties = map[string]any{}
	}
	status.Properties[StatusPropReplyToURI] = uri
}

// FindReplyParent resolves uri to a status already in the database.
// Local /users/:name/statuses/:id, /@name/status/:id and /@name/:id forms are
// recognized, as is any stored origin or object URI. Unknown addresses and
// remote objects that were never stored return gorm.ErrRecordNotFound.
// This function does not fetch.
func FindReplyParent(conn *gorm.DB, baseURL, raw string) (*Status, error) {
	uri := canonicalReplyURI(raw)
	if conn == nil || uri == "" {
		return nil, gorm.ErrRecordNotFound
	}
	if parent, err := parentByStoredURI(conn, uri); err == nil || !isNotFound(err) {
		return parent, err
	}
	if !sameReplyOrigin(uri, baseURL) {
		return nil, gorm.ErrRecordNotFound
	}
	if username, id, ok := localStatusPath(uri); ok {
		status, err := parentByID(conn, id)
		if err != nil {
			return nil, err
		}
		if statusMatchesLocalRef(status, baseURL, uri, username) {
			return status, nil
		}
	}
	if id, ok := trailingStatusID(uri); ok {
		status, err := parentByID(conn, id)
		if isNotFound(err) {
			return nil, gorm.ErrRecordNotFound
		}
		if err != nil {
			return nil, err
		}
		if status.ObjectURI(baseURL) == uri {
			return status, nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

// LinkStoredReplyParents writes in_reply_to_id for replies whose parent is
// already in the database. It uses a stored inReplyTo URI, or a single local
// status URL in the body. It does not fetch.
func LinkStoredReplyParents(conn *gorm.DB, statuses []*Status, baseURL string) error {
	if conn == nil {
		return nil
	}
	for _, status := range statuses {
		if !needsReplyLink(status) {
			continue
		}
		parent, err := storedParentFor(conn, status, baseURL)
		if err != nil {
			return err
		}
		if parent == nil {
			continue
		}
		if err = persistReplyLink(conn, status, parent); err != nil {
			return err
		}
	}
	return nil
}

// RepairUnlinkedReplyParents links every stored reply whose parent can be
// resolved locally. Safe to run more than once. It does not fetch.
func RepairUnlinkedReplyParents(conn *gorm.DB, baseURL string) (int, error) {
	if conn == nil || !conn.Migrator().HasTable(&Status{}) || !conn.Migrator().HasTable(&StatusContent{}) {
		return 0, nil
	}
	var ids []uint64
	if err := unlinkedReplyParentQuery(conn, baseURL).Pluck("statuses.id", &ids).Error; err != nil {
		return 0, err
	}
	linked := 0
	for start := 0; start < len(ids); start += 100 {
		end := start + 100
		if end > len(ids) {
			end = len(ids)
		}
		var batch []*Status
		if err := conn.Preload("Content").Preload("Account").Where("id IN ?", ids[start:end]).Find(&batch).Error; err != nil {
			return linked, err
		}
		before := linkedIDs(batch)
		if err := LinkStoredReplyParents(conn, batch, baseURL); err != nil {
			return linked, err
		}
		for _, status := range batch {
			if !before[status.Id] && status.InReplyToId != nil && *status.InReplyToId != 0 {
				linked++
			}
		}
	}
	return linked, nil
}

// unlinkedReplyParentQuery selects replies that may still need a local parent:
// a stored inReplyTo address, or a local status URL in the body.
func unlinkedReplyParentQuery(conn *gorm.DB, baseURL string) *gorm.DB {
	host := replyHost(baseURL)
	query := conn.Table("statuses").
		Joins("LEFT JOIN status_contents ON status_contents.status_id = statuses.id").
		Where("(statuses.in_reply_to_id IS NULL OR statuses.in_reply_to_id = 0) AND statuses.deleted_at IS NULL AND (statuses.reblog_id IS NULL OR statuses.reblog_id = 0)")
	// statuses.properties is jsonb on PostgreSQL. LIKE (~~) is not defined for
	// jsonb ("operator does not exist: jsonb ~~ unknown"), so match a text cast.
	props := statusPropertiesText(conn)
	if host != "" {
		return query.Where("status_contents.content LIKE ? OR "+props+" LIKE ?", "%"+host+"%", "%"+StatusPropReplyToURI+"%")
	}
	return query.Where(props+" LIKE ?", "%"+StatusPropReplyToURI+"%")
}

// statusPropertiesText is the text form of statuses.properties. MySQL casts
// JSON to CHAR; PostgreSQL and SQLite cast to TEXT.
func statusPropertiesText(conn *gorm.DB) string {
	if dialect.IsMySQL(conn) {
		return "CAST(statuses.properties AS CHAR)"
	}
	return "CAST(statuses.properties AS TEXT)"
}

func storedParentFor(conn *gorm.DB, status *Status, baseURL string) (*Status, error) {
	candidates := []string{}
	if uri := ReplyToURI(status); uri != "" {
		candidates = append(candidates, uri)
	} else if status.Content.Content != "" {
		candidates = append(candidates, localStatusURLs(status.Content.Content, baseURL)...)
	}
	var parent *Status
	seen := map[uint64]bool{}
	for _, candidate := range candidates {
		found, err := FindReplyParent(conn, baseURL, candidate)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if found == nil || found.Id == status.Id || seen[found.Id] {
			continue
		}
		seen[found.Id] = true
		if parent != nil && parent.Id != found.Id {
			return nil, nil
		}
		parent = found
	}
	return parent, nil
}

func persistReplyLink(conn *gorm.DB, status, parent *Status) error {
	status.InReplyToId = &parent.Id
	status.InReplyToAccountId = &parent.AccountId
	SetReplyToURI(status, "")
	return conn.Session(&gorm.Session{SkipHooks: true}).Model(status).Select("InReplyToId", "InReplyToAccountId", "Properties").Updates(status).Error
}

func needsReplyLink(status *Status) bool {
	if status == nil || (status.InReplyToId != nil && *status.InReplyToId != 0) {
		return false
	}
	return status.ReblogId == nil || *status.ReblogId == 0
}

func linkedIDs(statuses []*Status) map[uint64]bool {
	out := map[uint64]bool{}
	for _, status := range statuses {
		if status != nil && status.InReplyToId != nil && *status.InReplyToId != 0 {
			out[status.Id] = true
		}
	}
	return out
}

func parentByID(conn *gorm.DB, id uint64) (*Status, error) {
	var status Status
	err := conn.Preload("Account").Preload("ToAccounts").Where("id = ? AND deleted_at IS NULL", id).First(&status).Error
	if err != nil {
		return nil, err
	}
	return &status, nil
}

func trailingStatusID(uri string) (uint64, bool) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return 0, false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != "statuses" {
		return 0, false
	}
	id, err := strconv.ParseUint(parts[len(parts)-1], 10, 64)
	return id, err == nil && id > 0
}

func parentByStoredURI(conn *gorm.DB, uri string) (*Status, error) {
	var status Status
	err := conn.Preload("Account").Preload("ToAccounts").
		Where("deleted_at IS NULL AND (origin_uri = ? OR origin_url = ? OR local_object_uri = ?)", uri, uri, uri).
		First(&status).Error
	if err != nil {
		return nil, err
	}
	return &status, nil
}

func statusMatchesLocalRef(status *Status, baseURL, uri, username string) bool {
	if status == nil || status.Account == nil || status.DeletedAt != nil {
		return false
	}
	if status.ObjectURI(baseURL) == uri || status.LocalObjectURI == uri {
		return true
	}
	name, domain, _ := strings.Cut(username, "@")
	if name == "" || !strings.EqualFold(status.Account.Username, name) {
		return false
	}
	if domain == "" {
		return status.Account.IsLocal()
	}
	return status.Account.Domain != nil && strings.EqualFold(*status.Account.Domain, domain)
}

func localStatusPath(uri string) (string, uint64, bool) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", 0, false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	parseID := func(raw string) (uint64, bool) {
		id, err := strconv.ParseUint(raw, 10, 64)
		return id, err == nil && id > 0
	}
	switch {
	case len(parts) == 4 && parts[0] == "users" && parts[2] == "statuses":
		if id, ok := parseID(parts[3]); ok {
			return parts[1], id, true
		}
	case len(parts) == 3 && strings.HasPrefix(parts[0], "@") && parts[1] == "status":
		if id, ok := parseID(parts[2]); ok {
			return strings.TrimPrefix(parts[0], "@"), id, true
		}
	case len(parts) == 2 && strings.HasPrefix(parts[0], "@"):
		if id, ok := parseID(parts[1]); ok {
			return strings.TrimPrefix(parts[0], "@"), id, true
		}
	}
	return "", 0, false
}

func localStatusURLs(content, baseURL string) []string {
	if content == "" || replyHost(baseURL) == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, match := range statusURLInText.FindAllString(content, 8) {
		uri := canonicalReplyURI(strings.TrimRight(match, ".,);"))
		if uri == "" || !sameReplyOrigin(uri, baseURL) || seen[uri] {
			continue
		}
		if _, _, ok := localStatusPath(uri); !ok {
			continue
		}
		seen[uri] = true
		out = append(out, uri)
	}
	return out
}

func canonicalReplyURI(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	parsed.Fragment = ""
	parsed.RawQuery = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}

func sameReplyOrigin(uri, baseURL string) bool {
	left, right := replyHost(uri), replyHost(baseURL)
	return left != "" && strings.EqualFold(left, right)
}

func replyHost(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return parsed.Host
}

func isNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
