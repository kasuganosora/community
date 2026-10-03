// Package datatransfer implements explicit, auditable account data transfers.
// Source IDs are strings: PostgreSQL bigint and Misskey IDs must never pass
// through floating point, and must not become target database primary keys.
package datatransfer

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

const Schema = "community-transfer/v1"

type Source struct {
	Platform      string    `json:"platform"`
	Version       string    `json:"version,omitempty"`
	BaseURL       string    `json:"base_url"`
	AccountDomain string    `json:"account_domain,omitempty"`
	SnapshotID    string    `json:"snapshot_id,omitempty"`
	ExportedAt    time.Time `json:"exported_at"`
}

// Bundle is an offline manifest. Credentials/keys are legal only in site mode.
// Personal bundles identify a single owner; other accounts are references, not
// authority to create or take over their local identities.
type Bundle struct {
	Schema      string            `json:"schema"`
	Scope       string            `json:"scope"`
	Source      Source            `json:"source"`
	OwnerID     string            `json:"owner_id,omitempty"`
	Accounts    []Account         `json:"accounts,omitempty"`
	Credentials []Credential      `json:"credentials,omitempty"`
	Keys        []FederationKey   `json:"keys,omitempty"`
	Posts       []Post            `json:"posts,omitempty"`
	Media       []Media           `json:"media,omitempty"`
	Relations   []Relation        `json:"relations,omitempty"`
	Lists       []List            `json:"lists,omitempty"`
	Reactions   []Reaction        `json:"reactions,omitempty"`
	Bookmarks   []Bookmark        `json:"bookmarks,omitempty"`
	Polls       []Poll            `json:"polls,omitempty"`
	PollVotes   []PollVote        `json:"poll_votes,omitempty"`
	Emojis      []Emoji           `json:"emojis,omitempty"`
	Records     []PreservedRecord `json:"records,omitempty"`
	Issues      []Issue           `json:"issues,omitempty"`
}

type Account struct {
	ID           string         `json:"id"`
	Username     string         `json:"username"`
	Domain       string         `json:"domain,omitempty"`
	Local        bool           `json:"local"`
	System       bool           `json:"system,omitempty"`
	ActorURI     string         `json:"actor_uri"`
	URL          string         `json:"url,omitempty"`
	Inbox        string         `json:"inbox,omitempty"`
	SharedInbox  string         `json:"shared_inbox,omitempty"`
	DisplayName  string         `json:"display_name,omitempty"`
	Note         string         `json:"note,omitempty"`
	NoteHTML     bool           `json:"note_html,omitempty"`
	Fields       []Field        `json:"fields,omitempty"`
	AvatarID     string         `json:"avatar_id,omitempty"`
	HeaderID     string         `json:"header_id,omitempty"`
	AvatarURL    string         `json:"avatar_url,omitempty"`
	HeaderURL    string         `json:"header_url,omitempty"`
	EmojiIDs     []string       `json:"emoji_ids,omitempty"`
	Locked       bool           `json:"locked,omitempty"`
	Bot          bool           `json:"bot,omitempty"`
	Discoverable bool           `json:"discoverable,omitempty"`
	NoIndex      bool           `json:"noindex,omitempty"`
	Suspended    bool           `json:"suspended,omitempty"`
	Admin        bool           `json:"admin,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at,omitempty"`
	Settings     map[string]any `json:"settings,omitempty"`
}

type Field struct {
	Name       string     `json:"name"`
	Value      string     `json:"value"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
}

type Credential struct {
	AccountID      string          `json:"account_id"`
	Email          string          `json:"email"`
	EmailVerified  bool            `json:"email_verified"`
	PasswordHash   string          `json:"password_hash"`
	PasswordScheme string          `json:"password_scheme"`
	PasswordSalt   string          `json:"password_salt,omitempty"`
	Disabled       bool            `json:"disabled,omitempty"`
	TwoFactor      bool            `json:"two_factor,omitempty"`
	Extensions     json.RawMessage `json:"extensions,omitempty"`
}

type FederationKey struct {
	AccountID  string     `json:"account_id"`
	KeyID      string     `json:"key_id"`
	PublicKey  string     `json:"public_key"`
	PrivateKey string     `json:"private_key,omitempty"`
	Algorithm  string     `json:"algorithm,omitempty"`
	Primary    bool       `json:"primary,omitempty"`
	Revoked    bool       `json:"revoked,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
}

type Post struct {
	ID             string          `json:"id"`
	AuthorID       string          `json:"author_id"`
	URI            string          `json:"uri,omitempty"`
	URL            string          `json:"url,omitempty"`
	Text           string          `json:"text,omitempty"`
	HTML           string          `json:"html,omitempty"`
	SpoilerText    string          `json:"spoiler_text,omitempty"`
	Visibility     string          `json:"visibility"`
	LocalOnly      bool            `json:"local_only,omitempty"`
	Sensitive      bool            `json:"sensitive,omitempty"`
	Language       string          `json:"language,omitempty"`
	RecipientIDs   []string        `json:"recipient_ids,omitempty"`
	ReplyID        string          `json:"reply_id,omitempty"`
	ReblogID       string          `json:"reblog_id,omitempty"`
	MediaIDs       []string        `json:"media_ids,omitempty"`
	Tags           []string        `json:"tags,omitempty"`
	EmojiIDs       []string        `json:"emoji_ids,omitempty"`
	ConversationID string          `json:"conversation_id,omitempty"`
	Pinned         bool            `json:"pinned,omitempty"`
	ArchiveOnly    bool            `json:"archive_only,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at,omitempty"`
	Extensions     json.RawMessage `json:"extensions,omitempty"`
}

type Media struct {
	ID          string    `json:"id"`
	OwnerID     string    `json:"owner_id"`
	Path        string    `json:"path,omitempty"`
	SHA256      string    `json:"sha256,omitempty"`
	Size        int64     `json:"size"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	RemoteURL   string    `json:"remote_url,omitempty"`
	OriginalURL string    `json:"original_url,omitempty"`
	Description string    `json:"description,omitempty"`
	Blurhash    string    `json:"blurhash,omitempty"`
	Width       int       `json:"width,omitempty"`
	Height      int       `json:"height,omitempty"`
	FocusX      float64   `json:"focus_x,omitempty"`
	FocusY      float64   `json:"focus_y,omitempty"`
	CreatedAt   time.Time `json:"created_at,omitempty"`
}

type Relation struct {
	Kind         string     `json:"kind"` // follow, follow_request, block, mute, domain_block
	AccountID    string     `json:"account_id"`
	TargetID     string     `json:"target_id,omitempty"`
	TargetURI    string     `json:"target_uri,omitempty"`
	ActivityURI  string     `json:"activity_uri,omitempty"`
	Languages    []string   `json:"languages,omitempty"`
	TargetDomain string     `json:"target_domain,omitempty"`
	ShowReblogs  bool       `json:"show_reblogs"`
	Notify       bool       `json:"notify,omitempty"`
	CreatedAt    time.Time  `json:"created_at,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
}

type List struct {
	ID            string   `json:"id"`
	OwnerID       string   `json:"owner_id"`
	Title         string   `json:"title"`
	RepliesPolicy string   `json:"replies_policy,omitempty"`
	Exclusive     bool     `json:"exclusive,omitempty"`
	MemberIDs     []string `json:"member_ids"`
}

type Reaction struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	PostID    string    `json:"post_id"`
	Emoji     string    `json:"emoji,omitempty"` // empty means favourite
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type Bookmark struct {
	AccountID string `json:"account_id"`
	PostID    string `json:"post_id,omitempty"`
	PostURI   string `json:"post_uri,omitempty"`
}

type Poll struct {
	ID        string       `json:"id"`
	PostID    string       `json:"post_id"`
	Multiple  bool         `json:"multiple,omitempty"`
	ExpiresAt *time.Time   `json:"expires_at,omitempty"`
	Options   []PollOption `json:"options"`
}

type PollOption struct {
	Title string `json:"title"`
	Votes uint64 `json:"votes"`
}

type PollVote struct {
	PollID    string    `json:"poll_id"`
	AccountID string    `json:"account_id"`
	Option    int       `json:"option"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type Emoji struct {
	ID        string   `json:"id"`
	Shortcode string   `json:"shortcode"`
	Domain    string   `json:"domain,omitempty"`
	MediaID   string   `json:"media_id,omitempty"`
	URL       string   `json:"url,omitempty"`
	Category  string   `json:"category,omitempty"`
	Aliases   []string `json:"aliases,omitempty"`
	Disabled  bool     `json:"disabled,omitempty"`
	Sensitive bool     `json:"sensitive,omitempty"`
	License   string   `json:"license,omitempty"`
}

// PreservedRecord is private source data with no equivalent live feature.
// Its presence must be reported, never counted as an operational conversion.
type PreservedRecord struct {
	Kind    string          `json:"kind"`
	ID      string          `json:"id"`
	OwnerID string          `json:"owner_id,omitempty"`
	Data    json.RawMessage `json:"data"`
}

type Issue struct {
	Severity string `json:"severity"` // error, warning, info
	Code     string `json:"code"`
	Kind     string `json:"kind,omitempty"`
	SourceID string `json:"source_id,omitempty"`
	Message  string `json:"message"`
}

type Report struct {
	CanExecute bool           `json:"can_execute"`
	Source     Source         `json:"source"`
	Counts     map[string]int `json:"counts"`
	Created    map[string]int `json:"created"`
	Skipped    map[string]int `json:"skipped"`
	Archived   map[string]int `json:"archived"`
	Issues     []Issue        `json:"issues"`
}

type Progress struct {
	Phase     string `json:"phase"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
}

// Options contains trusted server-side values, never values accepted directly
// from a personal upload. ArchiveDir is the verified private extraction root.
type Options struct {
	Scope              string
	OwnerUserID        uint64
	OwnerAccountID     uint64
	TargetBaseURL      string
	ArchiveDir         string
	StorageDir         string
	Files              map[string]ArchiveFile
	VerifiedReferences map[string]bool
	Namespace          string
	JobID              string
	LeaseCheck         func(*gorm.DB) error
}
