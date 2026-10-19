package datatransfer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func transferNamespace(b *Bundle, o Options) string {
	sourceOrigin := strings.TrimRight(strings.ToLower(b.Source.BaseURL), "/")
	if sourceOrigin == "" {
		sourceOrigin, _ = digestValue(b)
	}
	ownerScope := ""
	if o.Scope == "personal" {
		ownerScope = fmt.Sprintf("\n%d\n%d", o.OwnerUserID, o.OwnerAccountID)
	}
	return digestBytes([]byte(strings.ToLower(b.Source.Platform) + "\n" + sourceOrigin + "\n" + o.Scope + ownerScope))
}
func safeHTTPURL(value string) bool {
	u, e := url.Parse(value)
	return e == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") && u.User == nil && u.Fragment == ""
}
func sameOrigin(a, b string) bool {
	x, e := url.Parse(a)
	if e != nil {
		return false
	}
	y, e := url.Parse(b)
	return e == nil && strings.EqualFold(x.Scheme, y.Scheme) && strings.EqualFold(x.Host, y.Host)
}

// reportSnapshotID accepts only the snapshot ids the exporters write (keyset
// fetch timestamps from export_misskey.py / export_mastodon_db.py, or a
// PostgreSQL txid_current_snapshot() from export_mastodon.rb); anything else
// in an untrusted upload is not echoed into the report.
var reportSnapshotID = regexp.MustCompile(`^(keyset:[0-9T:.+Z-]{10,40}/[0-9T:.+Z-]{10,40}(/final)?|[0-9]{1,20}:[0-9]{1,20}:[0-9,]{0,80})$`)

func newReport(b *Bundle) *Report {
	source := Source{Platform: b.Source.Platform}
	if safeHTTPURL(b.Source.BaseURL) {
		u, _ := url.Parse(b.Source.BaseURL)
		if u.RawQuery == "" {
			source.BaseURL = u.Scheme + "://" + u.Host
		}
	}
	if regexp.MustCompile(`^[a-zA-Z0-9.+_-]{1,64}$`).MatchString(b.Source.Version) {
		source.Version = b.Source.Version
	}
	// exported_at / snapshot_id tell operators (and verify_misskey_import.py
	// --bundle) when the source was read; without them the report showed
	// 0001-01-01 for every streamed or direct import.
	if !b.Source.ExportedAt.IsZero() {
		source.ExportedAt = b.Source.ExportedAt.UTC()
	}
	if reportSnapshotID.MatchString(b.Source.SnapshotID) {
		source.SnapshotID = b.Source.SnapshotID
	}
	if source.Platform != "community" && source.Platform != "mastodon" && source.Platform != "misskey" {
		source.Platform = "unknown"
	}
	return &Report{Source: source, CanExecute: true, Counts: map[string]int{}, Created: map[string]int{}, Skipped: map[string]int{}, Archived: map[string]int{}}
}
func issue(r *Report, code, kind, id, message string) {
	r.CanExecute = false
	r.Issues = append(r.Issues, Issue{Severity: "error", Code: code, Kind: kind, SourceID: id, Message: message})
}

func Preview(ctx context.Context, conn *gorm.DB, b *Bundle, o Options) (*Report, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b == nil {
		return nil, fmt.Errorf("missing transfer bundle")
	}
	conn = conn.Session(&gorm.Session{Logger: logger.Discard})
	r := newReport(b)
	r.Counts["relation"] = len(b.Relations)
	r.Counts["reaction"] = len(b.Reactions)
	r.Counts["bookmark"] = len(b.Bookmarks)
	r.Counts["poll_vote"] = len(b.PollVotes)
	r.Counts["record"] = len(b.Records)
	if b.Source.BaseURL == "" && o.Scope == "personal" {
		r.Issues = append(r.Issues, Issue{Severity: "warning", Code: "unknown_source_identity", Message: "source origin missing; deduplication is limited to this exact manifest"})
	}
	if b.Schema != Schema {
		issue(r, "schema", "", "", "unsupported transfer schema")
	}
	if (o.Scope != "site" && o.Scope != "personal") || b.Scope != o.Scope {
		issue(r, "scope", "", "", "bundle scope does not match trusted job scope")
	}
	if (b.Source.BaseURL != "" || o.Scope != "personal") && !safeHTTPURL(b.Source.BaseURL) || !safeHTTPURL(o.TargetBaseURL) {
		issue(r, "origin", "", "", "source and target require canonical HTTP origins")
	}
	if b.Source.Platform != "community" && b.Source.Platform != "mastodon" && b.Source.Platform != "misskey" {
		issue(r, "platform", "", "", "unsupported source platform")
	}
	if o.Scope == "site" && !sameOrigin(b.Source.BaseURL, o.TargetBaseURL) {
		issue(r, "origin", "", "", "site migration requires the original origin")
	}

	if o.Scope == "site" && b.Source.AccountDomain != "" {
		origin, _ := url.Parse(b.Source.BaseURL)
		if origin == nil || !strings.EqualFold(strings.TrimSuffix(b.Source.AccountDomain, "."), strings.TrimSuffix(origin.Hostname(), ".")) {
			issue(r, "unsupported_account_domain", "", "", "account domain must match the retained web origin")
		}
	}
	if o.Scope == "personal" {
		var owner models.Account
		if o.OwnerAccountID == 0 || o.OwnerUserID == 0 {
			issue(r, "owner", "", "", "personal import requires an authenticated owner")
		} else if err := conn.WithContext(ctx).Where("id = ? AND user_id = ? AND domain IS NULL", o.OwnerAccountID, o.OwnerUserID).First(&owner).Error; err != nil {
			if err != gorm.ErrRecordNotFound {
				return nil, err
			}
			issue(r, "owner", "", "", "personal owner is not a local account")
		}
		if len(b.Credentials) > 0 || len(b.Keys) > 0 {
			issue(r, "personal_credentials", "", "", "personal bundles cannot import credentials or signing keys")
		}
	}
	for _, v := range sourceIssues(b.Issues, o.Scope == "site") {
		r.Issues = append(r.Issues, v)
		if v.Severity == "error" {
			r.CanExecute = false
		}
	}
	ids := map[string]map[string]bool{}
	add := func(kind, id string) {
		r.Counts[kind]++
		if ids[kind] == nil {
			ids[kind] = map[string]bool{}
		}
		if id == "" || ids[kind][id] {
			issue(r, "duplicate_id", kind, id, "missing or duplicate source identifier")
		}
		ids[kind][id] = true
	}
	accounts := map[string]Account{}
	for _, a := range b.Accounts {
		add("account", a.ID)
		accounts[a.ID] = a
		if o.Scope == "site" && a.Local {
			// requireSigninToViewContents is imported as Account.RequireSigninToView.
			for _, key := range []string{"makeNotesFollowersOnlyBefore", "makeNotesHiddenBefore"} {
				if policyEnabled(a.Settings[key]) {
					issue(r, "unsupported_privacy_policy", "account", a.ID, privacyPolicyMessage(a, key))
				}
			}
		}

		if o.Scope == "site" && a.Local {
			for _, key := range []string{"isDeleted", "movedToUri", "movedAt", "alsoKnownAs", "source_requested_deletion_at", "source_deleted_at", "source_moved_to_account_id", "source_also_known_as", "source_memorial", "source_silenced_at"} {
				if policyEnabled(a.Settings[key]) {
					issue(r, "unsupported_account_lifecycle", "account", a.ID, "source account lifecycle or alias policy requires explicit target support")
				}
			}
		}
		if !(o.Scope == "personal" && (a.ID == b.OwnerID || !o.VerifiedReferences[a.ID])) && (a.Username == "" || len(a.Username) > 64) {
			issue(r, "username", "account", a.ID, "unsupported username length")
		}
		if !(o.Scope == "personal" && (a.ID == b.OwnerID || !o.VerifiedReferences[a.ID])) && !safeHTTPURL(a.ActorURI) {
			issue(r, "actor_uri", "account", a.ID, "account actor URI is required")
		}
		if o.Scope == "site" && a.Local && !sameOrigin(a.ActorURI, o.TargetBaseURL) {
			issue(r, "actor_origin", "account", a.ID, "local actor must retain target origin")
		}
	}
	for _, p := range b.Posts {
		add("post", p.ID)
	}
	for _, m := range b.Media {
		add("media", m.ID)
	}
	for _, p := range b.Polls {
		add("poll", p.ID)
	}
	for _, l := range b.Lists {
		add("list", l.ID)
	}
	for _, e := range b.Emojis {
		add("emoji", e.ID)
	}
	ref := func(kind, id, source string) {
		if id != "" && !ids[kind][id] && o.Seed[kind][id] == 0 && !(kind == "post" && o.Seed[ArchivedPostKind][id] != 0) {
			issue(r, "missing_reference", kind, source, "referenced source record is missing")
		}
	}
	for _, a := range b.Accounts {
		for _, id := range a.EmojiIDs {
			ref("emoji", id, a.ID)
		}
		ref("media", a.AvatarID, a.ID)
		ref("media", a.HeaderID, a.ID)
		if a.AvatarURL != "" && a.AvatarID == "" && !safeHTTPURL(a.AvatarURL) || a.HeaderURL != "" && a.HeaderID == "" && !safeHTTPURL(a.HeaderURL) {
			issue(r, "profile_media", "account", a.ID, "unsupported profile image URL")
		}
	}
	if o.Scope == "personal" && !ids["account"][b.OwnerID] {
		issue(r, "owner", "account", b.OwnerID, "bundle owner account is missing")
	}
	credentials := map[string]Credential{}
	keys := map[string]int{}
	primary := map[string]int{}
	for _, c := range b.Credentials {
		add("credential", c.AccountID)
		ref("account", c.AccountID, c.AccountID)
		credentials[c.AccountID] = c
		if !accounts[c.AccountID].Local {
			issue(r, "credential_owner", "credential", c.AccountID, "credentials belong only to local accounts")
		}
		if tf, reason := parseCredentialTwoFactor(c); reason != "" {
			issue(r, "unsupported_auth", "credential", c.AccountID, reason+": "+authReasonText[reason])
		} else if tf != nil && tf.SecurityKeys {
			r.Issues = append(r.Issues, Issue{Severity: "warning", Code: "security_keys_separate", Kind: "credential", SourceID: c.AccountID,
				Message: "security keys are not in the bundle; run `tootctl passkeys import-misskey` right after the import"})
		}
		if c.PasswordScheme == "bcrypt" {
			if models.ValidateImportedBcrypt(c.PasswordHash) != nil {
				issue(r, "password", "credential", c.AccountID, "invalid imported bcrypt credential")
			}
		} else if c.PasswordScheme != "community" || c.PasswordHash == "" || c.PasswordSalt == "" {
			issue(r, "password_scheme", "credential", c.AccountID, "unsupported password scheme")
		}
		if c.Email == "" && c.EmailVerified {
			issue(r, "email", "credential", c.AccountID, "a missing email cannot be marked verified")
		}
	}
	for _, c := range b.Credentials {
		if c.PasswordScheme == "community" {
			decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(c.PasswordHash, "v2$"))
			if err != nil || models.ValidateImportedBcrypt(string(decoded)) != nil {
				issue(r, "password", "credential", c.AccountID, "invalid native password hash")
			}
		}
	}
	for _, k := range b.Keys {
		add("key", k.AccountID+"\n"+k.KeyID)
		ref("account", k.AccountID, k.AccountID)
		if !accounts[k.AccountID].Local {
			if k.PrivateKey != "" {
				issue(r, "key_owner", "key", k.AccountID, "remote key must not include private signing material")
			}
			continue
		}
		if k.Revoked || (k.ExpiresAt != nil && !k.ExpiresAt.After(time.Now())) {
			continue
		}
		if k.Algorithm != "" && k.Algorithm != "rsa" && k.Algorithm != "rsa-sha256" {
			issue(r, "key_algorithm", "key", k.AccountID, "unsupported active signing key algorithm")
			continue
		}
		key := models.AccountKey{PublicKey: k.PublicKey, PrivateKey: k.PrivateKey}
		if key.ValidateKeyPair() != nil || !safeHTTPURL(strings.SplitN(k.KeyID, "#", 2)[0]) {
			issue(r, "key", "key", k.AccountID, "invalid signing key pair or key identifier")
		} else {
			keys[k.AccountID]++
			if k.Primary {
				primary[k.AccountID]++
			}
		}
	}
	if o.Scope == "site" {
		for _, a := range b.Accounts {
			if a.Local {
				if _, ok := credentials[a.ID]; !ok && !a.System {
					issue(r, "missing_credentials", "account", a.ID, "local login account is missing credentials")
				}
				if keys[a.ID] == 0 || primary[a.ID] > 1 {
					issue(r, "missing_keys", "account", a.ID, "local actor needs an unambiguous active signing key")
				}
			}
		}
	}
	for _, m := range b.Media {

		if o.Scope == "site" && m.OriginalURL != "" {
			if _, err := MediaAliasURL(o.TargetBaseURL, m.OriginalURL); err != nil || m.Path == "" {
				issue(r, "media_alias", "media", m.ID, "original media URL requires a retained same-origin media route and local archive bytes")
			}
		}
		owner := sharedMediaOwner(b, m)
		ref("account", owner, m.ID)
		if owner == "" {
			issue(r, "media_owner", "media", m.ID, "media requires an owner or a known site asset reference")
		}
		if m.Path != "" {
			file, ok := o.Files[m.Path]
			if !ok || file.Path == "" || file.SHA256 == "" || m.SHA256 != "" && !strings.EqualFold(m.SHA256, file.SHA256) || m.Size > 0 && m.Size != file.Size {
				issue(r, "media_file", "media", m.ID, "verified archive media is missing or its checksum differs")
			}
		}
		referenced := m.StorageReference && o.Scope == "site" && m.RemoteURL != ""
		if m.Path == "" && (m.RemoteURL == "" || (accounts[m.OwnerID].Local && !referenced)) {
			issue(r, "media_missing", "media", m.ID, "local media requires original archive bytes")
		}
		if m.RemoteURL != "" && !safeHTTPURL(m.RemoteURL) {
			issue(r, "media_url", "media", m.ID, "invalid remote media URL")
		}
	}
	for _, p := range b.Posts {
		willArchive := p.ArchiveOnly || (o.Scope == "personal" && p.AuthorID != b.OwnerID)
		if o.Scope == "personal" {
			for _, id := range p.RecipientIDs {
				if id != b.OwnerID && ids["account"][id] && !o.VerifiedReferences[id] {
					willArchive = true
				}
			}
		}
		if willArchive {
			r.Archived["post"]++
			continue
		}
		ref("account", p.AuthorID, p.ID)
		ref("post", p.ReplyID, p.ID)
		ref("post", p.ReblogID, p.ID)
		switch p.Visibility {
		case "public", "unlisted", "private", "followers", "direct":
		default:
			issue(r, "visibility", "post", p.ID, "unsupported audience cannot be widened")
		}
		if p.Visibility == "direct" && len(p.RecipientIDs) == 0 && !p.ArchiveOnly {
			issue(r, "recipients", "post", p.ID, "direct message has no explicit recipients")
		}
		for _, id := range p.RecipientIDs {
			ref("account", id, p.ID)
		}
		for _, id := range p.MediaIDs {
			ref("media", id, p.ID)
		}
		for _, id := range p.EmojiIDs {
			ref("emoji", id, p.ID)
		}
		if o.Scope == "site" && accounts[p.AuthorID].Local && !p.ArchiveOnly && !(p.LocalOnly && p.URI == "") && (!safeHTTPURL(p.URI) || !sameOrigin(p.URI, o.TargetBaseURL)) {
			issue(r, "object_uri", "post", p.ID, "local object URI must preserve source identity")
		}
	}
	for _, v := range b.Relations {
		ref("account", v.AccountID, v.AccountID)
		ref("account", v.TargetID, v.AccountID)
		if o.Scope == "personal" && v.AccountID != b.OwnerID {
			issue(r, "relation_owner", "relation", v.AccountID, "personal archive cannot authorize another account's relationship")
		}
		switch v.Kind {
		case "follow", "follow_request", "block", "mute", "domain_block":
		default:
			issue(r, "relation_kind", "relation", v.AccountID, "unsupported relation kind")
		}
	}
	for _, v := range b.Lists {
		ref("account", v.OwnerID, v.ID)
		for _, a := range v.MemberIDs {
			ref("account", a, v.ID)
		}
		if o.Scope == "personal" && v.OwnerID != b.OwnerID {
			issue(r, "list_owner", "list", v.ID, "personal list belongs to another account")
		}
	}
	for _, v := range b.Polls {
		ref("post", v.PostID, v.ID)
		if len(v.Options) < 2 || len(v.Options) > 255 {
			issue(r, "poll_options", "poll", v.ID, "unsupported poll option count")
		}
	}
	for _, v := range b.PollVotes {
		ref("poll", v.PollID, v.PollID)
		ref("account", v.AccountID, v.PollID)
	}
	for _, v := range b.Reactions {
		ref("post", v.PostID, v.ID)
		ref("account", v.AccountID, v.ID)
	}
	for _, v := range b.Bookmarks {
		ref("post", v.PostID, v.PostID)
		ref("account", v.AccountID, v.PostID)
	}
	for _, v := range b.Emojis {
		ref("media", v.MediaID, v.ID)
	}
	if len(b.Records) > 0 {
		r.Issues = append(r.Issues, Issue{Severity: "warning", Code: "private_archive", Message: "unsupported records will remain private archives, not live features"})
	}
	if err := validateExistingReceipts(ctx, conn, b, o, r); err != nil {
		return r, err
	}
	if err := checkColumnWidths(conn.WithContext(ctx), b, o.Widths, r); err != nil {
		return r, err
	}
	narrowed, err := replyAudiences(ctx, conn, b, o, o.ParentVisibility)
	if err != nil {
		return r, err
	}
	if len(narrowed) > 0 {
		r.Issues = append(r.Issues, replyNarrowedIssue(len(narrowed), firstN(narrowed, maxWidthSamples), narrowed))
	}
	return r, nil
}

func policyEnabled(v any) bool {
	if v == nil {
		return false
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Slice, reflect.Map, reflect.Array, reflect.String:
		return r.Len() > 0
	default:
		return !r.IsZero()
	}
}

// privacyPolicyMessage names the source field and value so operators can
// decide on a mapping without querying the source database again.
func privacyPolicyMessage(a Account, key string) string {
	raw, _ := json.Marshal(a.Settings[key])
	who := a.ID
	if a.Username != "" {
		who = "@" + a.Username + " (" + a.ID + ")"
	}
	return fmt.Sprintf("source account %s has %s=%s; the target has no equivalent visibility policy", who, key, raw)
}
