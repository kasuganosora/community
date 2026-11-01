package seo

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/moderation"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

// Resolve decides the status code and <head> for a frontend path.
//
// Public profiles, tags, posts, the homepage, and about are indexable:
// title, description, Open Graph / Twitter cards, and a canonical URL, and
// no robots noindex. Missing profiles, tags, and posts are a real HTTP 404
// (not a 200 shell that search engines treat as a soft 404). Admin,
// settings, and other signed-in surfaces stay noindex. A nil database keeps
// the previous shell for entity URLs so the process can still boot.
func Resolve(conn *gorm.DB, requestPath string) Document {
	p := cleanPath(requestPath)
	site := siteName()
	base := strings.TrimRight(systemsettings.GetBaseURL(), "/")
	kind, arg := classify(p)
	switch kind {
	case routeHome:
		return homeDoc(base, site)
	case routeAbout:
		return aboutDoc(base, site)
	case routeExplore:
		return pageDoc(base, site, "/explore", "Explore", "Public posts on "+site+".", "website", false)
	case routeTopics:
		return pageDoc(base, site, "/explore/topics", "Topics", "Hashtags people are posting about on "+site+".", "website", false)
	case routeAnnouncements:
		return pageDoc(base, site, "/announcements", "Announcements", "Announcements from "+site+".", "website", false)
	case routeExploreAlias:
		return pageDoc(base, site, "/explore", "Explore", "Public posts on "+site+".", "website", false)
	case routePrivate:
		return Document{Code: 200, Meta: Meta{Title: site, SiteName: site, NoIndex: true}}
	case routeBare:
		return Document{Code: 200}
	case routeNotFound:
		return notFound(site)
	case routeTag:
		if conn == nil {
			return Document{Code: 200}
		}
		return resolveTag(conn, base, site, arg.name)
	case routeProfile:
		if conn == nil {
			return Document{Code: 200}
		}
		return resolveProfile(conn, base, site, arg.acct)
	case routeStatus:
		if conn == nil {
			return Document{Code: 200}
		}
		return resolveStatus(conn, base, site, arg.acct, arg.id)
	case routeCollection:
		if conn == nil {
			return Document{Code: 200}
		}
		return resolveCollection(conn, base, site, arg.acct, arg.collection)
	default:
		return notFound(site)
	}
}

type routeKind int

const (
	routeNotFound routeKind = iota
	routeHome
	routeAbout
	routeExplore
	routeTopics
	routeAnnouncements
	routeExploreAlias
	routePrivate
	routeBare
	routeTag
	routeProfile
	routeStatus
	routeCollection
)

type routeArg struct {
	name       string
	acct       string
	id         uint64
	collection string
}

func classify(p string) (routeKind, routeArg) {
	switch p {
	case "/":
		return routeHome, routeArg{}
	case "/about":
		return routeAbout, routeArg{}
	case "/explore":
		return routeExplore, routeArg{}
	case "/explore/topics":
		return routeTopics, routeArg{}
	case "/announcements":
		return routeAnnouncements, routeArg{}
	case "/public", "/public/local", "/federated", "/timeline/public":
		return routeExploreAlias, routeArg{}
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return routeNotFound, routeArg{}
	}
	switch parts[0] {
	case "admin", "settings", "login", "register", "notifications", "messages", "bookmarks", "lists", "search", "appeal", "groups":
		return routePrivate, routeArg{}
	case "notes":
		// ActivityPub owns /notes/:id. If the shell is still asked for one,
		// do not pretend the post is missing.
		return routeBare, routeArg{}
	case "tags":
		if len(parts) != 2 || strings.Trim(parts[1], "#") == "" {
			return routeNotFound, routeArg{}
		}
		return routeTag, routeArg{name: strings.TrimPrefix(parts[1], "#")}
	}
	if !strings.HasPrefix(parts[0], "@") {
		return routeNotFound, routeArg{}
	}
	acct := strings.TrimPrefix(parts[0], "@")
	switch len(parts) {
	case 1:
		return routeProfile, routeArg{acct: acct}
	case 2:
		if parts[1] == "followers" || parts[1] == "following" {
			return routeCollection, routeArg{acct: acct, collection: parts[1]}
		}
		if id, ok := parseID(parts[1]); ok {
			return routeStatus, routeArg{acct: acct, id: id}
		}
	case 3:
		if parts[1] == "status" {
			if id, ok := parseID(parts[2]); ok {
				return routeStatus, routeArg{acct: acct, id: id}
			}
		}
	}
	return routeNotFound, routeArg{}
}

func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	cleaned := p
	if c := strings.TrimSuffix(p, "/"); c != "" {
		cleaned = c
	}
	// path.Clean would rewrite "@" routes; only drop a trailing slash and
	// collapse duplicate slashes.
	for strings.Contains(cleaned, "//") {
		cleaned = strings.ReplaceAll(cleaned, "//", "/")
	}
	if cleaned == "" {
		return "/"
	}
	return cleaned
}

func parseID(s string) (uint64, bool) {
	id, err := strconv.ParseUint(s, 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

func siteName() string {
	name := strings.TrimSpace(systemsettings.GetString(systemsettings.KeyTitle, ""))
	if name == "" {
		return "Community"
	}
	return name
}

func siteBlurb() string {
	return strings.TrimSpace(systemsettings.GetString(systemsettings.KeyDescription, ""))
}

func homeDoc(base, site string) Document {
	return Document{Code: 200, Meta: Meta{
		Title:       site,
		Description: withDiscovery(siteBlurb(), homeDiscovery),
		Keywords:    siteKeywords,
		Canonical:   base + "/",
		Type:        "website",
		Image:       siteImage(base),
		SiteName:    site,
	}}
}

func aboutDoc(base, site string) Document {
	return Document{Code: 200, Meta: Meta{
		Title:       "About - " + site,
		Description: withDiscovery(siteBlurb(), aboutDiscovery),
		Keywords:    siteKeywords,
		Canonical:   base + "/about",
		Type:        "website",
		Image:       siteImage(base),
		SiteName:    site,
	}}
}

func pageDoc(base, site, path, title, description, kind string, noindex bool) Document {
	return Document{Code: 200, Meta: Meta{
		Title:       title + " - " + site,
		Description: description,
		Canonical:   base + path,
		Type:        kind,
		Image:       siteImage(base),
		SiteName:    site,
		NoIndex:     noindex,
	}}
}

func notFound(site string) Document {
	return Document{Code: 404, Meta: Meta{
		Title:    "Page not found - " + site,
		SiteName: site,
		NoIndex:  true,
	}}
}

func siteImage(base string) string {
	b := systemsettings.GetBranding()
	for _, raw := range []string{b.BannerURL, b.IconURL, b.App512IconURL} {
		if abs := absoluteResource(base, raw); abs != "" {
			return abs
		}
	}
	return ""
}

func absoluteResource(base, raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "https://") || strings.HasPrefix(raw, "http://") {
		return raw
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return strings.TrimRight(base, "/") + raw
	}
	return ""
}

func resolveTag(conn *gorm.DB, base, site, name string) Document {
	tag, err := models.FindTagByName(conn, name)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && tag.StatusCount == 0) {
		return notFound(site)
	}
	if err != nil {
		return Document{Code: 200}
	}
	meta := Meta{
		Title:       "#" + tag.Name + " - " + site,
		Description: "Public posts tagged #" + tag.Name + " on " + site + ".",
		Canonical:   base + "/tags/" + url.PathEscape(tag.Name),
		Type:        "website",
		SiteName:    site,
	}
	// Hidden tags and visitor-blocked timelines stay out of the index.
	// Ordinary public tags do not get noindex: they are listed in the sitemap.
	if moderation.Current().IsHiddenTag(tag.Name) || moderation.Current().UGCVisibilityForVisitor == moderation.UGCNone {
		meta.NoIndex = true
		meta.Description = "Posts tagged #" + tag.Name + "."
	}
	return Document{Code: 200, Meta: meta}
}

func resolveProfile(conn *gorm.DB, base, site, acct string) Document {
	account, missing, err := findAccount(conn, acct)
	if err != nil {
		return Document{Code: 200}
	}
	if missing {
		return notFound(site)
	}
	return profileDoc(base, site, account)
}

func profileDoc(base, site string, account *models.Account) Document {
	name := strings.TrimSpace(account.DisplayName)
	if name == "" {
		name = account.Username
	}
	handle := "@" + account.Username
	if account.Domain != nil && *account.Domain != "" {
		handle += "@" + *account.Domain
	}
	meta := Meta{
		Title:     name + " (" + handle + ") - " + site,
		Canonical: base + profilePath(account),
		Type:      "profile",
		Image:     absoluteResource(base, first(account.Header, account.Avatar)),
		SiteName:  site,
	}
	meta.Description = profileDescription(site, name, account)
	meta.NoIndex = !profileIndexable(account)
	return Document{Code: 200, Meta: meta}
}

func profileDescription(site, name string, account *models.Account) string {
	if !guestCanSee(account) {
		return "Profile on " + site + "."
	}
	if note := excerpt(plainNote(account), 200); note != "" {
		return note
	}
	return "Posts by " + name + " on " + site + "."
}

func plainNote(account *models.Account) string {
	if account.IsLocal() && account.ImportedNoteHTML == "" {
		return account.Note
	}
	return htmlsafe.PlainText(htmlsafe.Sanitize(account.Note))
}

// profileIndexable is the sitemap rule for a local account: visitors can
// see it, and the account has not opted out of indexing.
func profileIndexable(account *models.Account) bool {
	if account == nil || account.IsBan || account.DeletionRequestedAt != nil || account.NoIndex || account.RequireSigninToView {
		return false
	}
	if !guestCanSee(account) {
		return false
	}
	return account.Domain == nil
}

func guestCanSee(account *models.Account) bool {
	host := ""
	if account != nil && account.Domain != nil {
		host = *account.Domain
	}
	return moderation.Current().GuestCanSee(host)
}

func profilePath(account *models.Account) string {
	if account.Domain != nil && *account.Domain != "" {
		return "/@" + url.PathEscape(account.Username) + "@" + url.PathEscape(*account.Domain)
	}
	return "/@" + url.PathEscape(account.Username)
}

func resolveCollection(conn *gorm.DB, base, site, acct, kind string) Document {
	account, missing, err := findAccount(conn, acct)
	if err != nil {
		return Document{Code: 200}
	}
	if missing {
		return notFound(site)
	}
	label := "Followers"
	if kind == "following" {
		label = "Following"
	}
	return Document{Code: 200, Meta: Meta{
		Title:     label + " - " + site,
		Canonical: base + profilePath(account) + "/" + kind,
		Type:      "website",
		SiteName:  site,
		NoIndex:   true,
	}}
}

func resolveStatus(conn *gorm.DB, base, site, acct string, id uint64) Document {
	account, missing, err := findAccount(conn, acct)
	if err != nil {
		return Document{Code: 200}
	}
	if missing {
		return notFound(site)
	}
	var status models.Status
	err = conn.Preload("Account").Preload("Content").Preload("Attachments").
		Where("id = ? AND account_id = ? AND deleted_at IS NULL AND scheduled_at IS NULL", id, account.Id).
		First(&status).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return notFound(site)
	}
	if err != nil {
		return Document{Code: 200}
	}
	if status.Account == nil {
		status.Account = account
	}
	shown := &status
	if status.ReblogId != nil && *status.ReblogId > 0 {
		var original models.Status
		err = conn.Preload("Account").Preload("Content").Preload("Attachments").
			Where("id = ? AND deleted_at IS NULL AND scheduled_at IS NULL", *status.ReblogId).
			First(&original).Error
		if err != nil {
			return notFound(site)
		}
		if original.Account == nil || original.Account.IsBan || original.Account.DeletionRequestedAt != nil ||
			original.Visibility == models.StatusVisibilityPrivate || original.Visibility == models.StatusVisibilityDirect {
			return notFound(site)
		}
		shown = &original
	}
	if status.Visibility == models.StatusVisibilityPrivate || status.Visibility == models.StatusVisibilityDirect {
		return notFound(site)
	}
	return statusDoc(base, site, &status, shown)
}

func statusDoc(base, site string, requested, shown *models.Status) Document {
	author := shown.Account
	if author == nil {
		author = requested.Account
	}
	name := "Post"
	if author != nil && strings.TrimSpace(author.DisplayName) != "" {
		name = strings.TrimSpace(author.DisplayName)
	} else if author != nil {
		name = author.Username
	}
	canonical := base + profilePath(requested.Account) + "/status/" + strconv.FormatUint(requested.Id, 10)
	if requested.ReblogId != nil && shown.Account != nil && shown.Account.Domain == nil && shown.Visibility == models.StatusVisibilityPublic {
		canonical = base + profilePath(shown.Account) + "/status/" + strconv.FormatUint(shown.Id, 10)
	}
	if remote := remoteWebURL(shown); remote != "" && (shown.Account == nil || shown.Account.Domain != nil) {
		canonical = remote
	}
	meta := Meta{
		Title:     statusTitle(name, "", site),
		Canonical: canonical,
		Type:      "article",
		SiteName:  site,
		NoIndex:   !statusIndexable(requested, shown),
	}
	if !statusTextHidden(requested, shown) {
		text := excerpt(statusPlain(shown), 200)
		meta.Description = text
		meta.Title = statusTitle(name, text, site)
		if !shown.Sensitive {
			meta.Image = statusImage(base, shown)
		}
	} else {
		meta.Description = "This post is only visible after signing in."
	}
	if meta.Image == "" && author != nil && statusIndexable(requested, shown) {
		meta.Image = absoluteResource(base, first(author.Avatar, author.Header))
	}
	return Document{Code: 200, Meta: meta}
}

func statusTitle(name, text, site string) string {
	text = excerpt(text, 80)
	if text == "" {
		return name + " - " + site
	}
	return name + ": " + text + " - " + site
}

func statusPlain(status *models.Status) string {
	if cw := strings.TrimSpace(status.SpoilerTextPlain()); cw != "" || status.Sensitive {
		if cw == "" {
			return "Sensitive content"
		}
		return cw
	}
	rendered := status.ContentHTML(htmlsafe.RenderOptions{})
	if strings.TrimSpace(rendered) == "" {
		return status.Content.Content
	}
	return htmlsafe.PlainText(rendered)
}

func statusTextHidden(requested, shown *models.Status) bool {
	if requested.Account != nil && requested.Account.RequireSigninToView {
		return true
	}
	if shown.Account != nil && shown.Account.RequireSigninToView {
		return true
	}
	if !guestCanSee(requested.Account) || !guestCanSee(shown.Account) {
		return true
	}
	return false
}

func statusIndexable(requested, shown *models.Status) bool {
	if statusTextHidden(requested, shown) {
		return false
	}
	if requested.Visibility != models.StatusVisibilityPublic || shown.Visibility != models.StatusVisibilityPublic {
		return false
	}
	if requested.ReblogId != nil && *requested.ReblogId > 0 {
		return false
	}
	author := shown.Account
	if author == nil || author.Domain != nil || author.NoIndex || author.IsBan {
		return false
	}
	return true
}

func statusImage(base string, status *models.Status) string {
	for _, media := range status.Attachments {
		if media == nil || media.Sensitive || media.Type != models.MediaTypeImage || media.FilePath == "" || media.FileName == "" {
			continue
		}
		return base + "/media/" + strconv.FormatUint(media.Id, 10) + "/" + url.PathEscape(media.FileName)
	}
	return ""
}

func remoteWebURL(status *models.Status) string {
	if status.OriginUrl != nil {
		if abs := httpURL(*status.OriginUrl); abs != "" {
			return abs
		}
	}
	return ""
}

func httpURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	return u.String()
}

func findAccount(conn *gorm.DB, raw string) (*models.Account, bool, error) {
	username, domain := splitAcct(raw)
	if username == "" || strings.ContainsAny(username, " /?#") {
		return nil, true, nil
	}
	var account models.Account
	err := models.WhereAcct(conn.Model(&models.Account{}), username, domain, false).Order("accounts.id ASC").First(&account).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if account.IsBan || account.DeletionRequestedAt != nil {
		return nil, true, nil
	}
	return &account, false, nil
}

func splitAcct(raw string) (username, domain string) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "@"))
	username, domain, ok := strings.Cut(raw, "@")
	if !ok {
		return raw, ""
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" || domain == strings.ToLower(systemsettings.GetDomain()) {
		return username, ""
	}
	return username, domain
}
