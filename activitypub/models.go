package activitypub

import "time"

// Actor represents an ActivityPub Actor.
type Actor struct {
	Discoverable    *bool `json:"discoverable,omitempty"`
	Indexable       *bool `json:"indexable,omitempty"`
	HideCollections *bool `json:"hideCollections,omitempty"`
	// Presence and validity are retained separately for partial actor Updates.
	discoveryPresent          map[string]bool
	discoveryInvalid          bool
	PublicKeys                []*PublicKey `json:"-"`
	Context                   any          `json:"@context,omitempty"`
	ID                        string       `json:"id"`
	Type                      string       `json:"type"`
	Following                 string       `json:"following"`
	Followers                 string       `json:"followers"`
	Inbox                     string       `json:"inbox"`
	Outbox                    string       `json:"outbox"`
	SharedInbox               string       `json:"sharedInbox,omitempty"`
	ManuallyApprovesFollowers bool         `json:"manuallyApprovesFollowers"`
	PreferredUsername         string       `json:"preferredUsername"`
	Name                      string       `json:"name,omitempty"`
	Summary                   string       `json:"summary,omitempty"`
	URL                       string       `json:"url"`
	Icon                      *Image       `json:"icon,omitempty"`
	Image                     *Image       `json:"image,omitempty"`
	Endpoints                 *Endpoints   `json:"endpoints,omitempty"`
	PublicKey                 *PublicKey   `json:"publicKey,omitempty"`
	Published                 *time.Time   `json:"published,omitempty"`
	Tag                       any          `json:"tag,omitempty"`
	Attachments               []Attachment `json:"attachment,omitempty"`
	Aliases                   []string     `json:"-"`
	AlsoKnownAs               string       `json:"alsoKnownAs,omitempty"`
	MovedTo                   string       `json:"movedTo,omitempty"`
}

// Endpoints contains additional ActivityPub endpoints.
type Endpoints struct {
	SharedInbox                string `json:"sharedInbox,omitempty"`
	OauthAuthorizationEndpoint string `json:"oauthAuthorizationEndpoint,omitempty"`
	OauthTokenEndpoint         string `json:"oauthTokenEndpoint,omitempty"`
	ProvideClientKey           string `json:"provideClientKey,omitempty"`
	SignClientKey              string `json:"signClientKey,omitempty"`
}

// PublicKey represents an actor's public key.
type PublicKey struct {
	ID           string `json:"id"`
	Owner        string `json:"owner"`
	PublicKeyPem string `json:"publicKeyPem"`
}

// Image represents an image attachment.
type Image struct {
	Type      string `json:"type"`
	MediaType string `json:"mediaType,omitempty"`
	URL       string `json:"url"`
	Name      string `json:"name,omitempty"`
}

// Attachment represents a profile field attachment.
type Attachment struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Note represents an ActivityPub Note object.
type Note struct {
	Context       any          `json:"@context,omitempty"`
	ID            string       `json:"id"`
	Type          string       `json:"type"`
	Summary       string       `json:"summary,omitempty"`
	InReplyTo     string       `json:"inReplyTo,omitempty"`
	Published     *time.Time   `json:"published"`
	Updated       *time.Time   `json:"updated,omitempty"`
	URL           string       `json:"url"`
	AttributedTo  string       `json:"attributedTo"`
	To            []string     `json:"to"`
	CC            []string     `json:"cc"`
	Bto           []string     `json:"bto,omitempty"`
	Bcc           []string     `json:"bcc,omitempty"`
	Sensitive     bool         `json:"sensitive"`
	Content       string       `json:"content"`
	Source        *Source      `json:"source,omitempty"`
	Attachment    []Attachment `json:"attachment,omitempty"`
	Tag           []Tag        `json:"tag,omitempty"`
	InReplyToAtom *string      `json:"inReplyToAtom,omitempty"`
	Conversation  string       `json:"conversation,omitempty"`
}

// Source represents the source property for Note objects
// Per ActivityPub spec, it contains the original content and media type.
type Source struct {
	Content   string `json:"content"`
	MediaType string `json:"mediaType"`
}

// Tag represents a mention, hashtag, or emoji.
type EmojiLicense struct {
	FreeText string `json:"freeText"`
}

type Tag struct {
	ID             string        `json:"id,omitempty"`
	Updated        string        `json:"updated,omitempty"`
	MisskeyLicense *EmojiLicense `json:"_misskey_license,omitempty"`
	Type           string        `json:"type"`
	Href           string        `json:"href,omitempty"`
	Name           string        `json:"name"`
	Icon           *Image        `json:"icon,omitempty"`
}

// Activity represents a generic ActivityPub activity.
type Activity struct {
	Context   any        `json:"@context,omitempty"`
	ID        string     `json:"id"`
	Type      string     `json:"type"`
	Actor     string     `json:"actor"`
	Object    any        `json:"object"`
	To        []string   `json:"to,omitempty"`
	CC        []string   `json:"cc,omitempty"`
	Published *time.Time `json:"published,omitempty"`
}

// CreateActivity represents a Create activity.
type CreateActivity struct {
	Activity
}

// UpdateActivity represents an Update activity.
type UpdateActivity struct {
	Activity
}

// DeleteActivity represents a Delete activity.
type DeleteActivity struct {
	Activity
}

// FollowActivity represents a Follow activity.
type FollowActivity struct {
	Activity
}

// AcceptActivity represents an Accept activity.
type AcceptActivity struct {
	Activity
}

// RejectActivity represents a Reject activity.
type RejectActivity struct {
	Activity
}

// AnnounceActivity represents an Announce (boost) activity.
type AnnounceActivity struct {
	Activity
}

// LikeActivity represents a Like (favourite) activity.
type LikeActivity struct {
	Activity
}

// UndoActivity represents an Undo activity.
type UndoActivity struct {
	Activity
}

// BlockActivity represents a Block activity.
type BlockActivity struct {
	Activity
}

// Collection represents an ActivityPub collection.
type Collection struct {
	Context      any    `json:"@context,omitempty"`
	ID           string `json:"id"`
	Type         string `json:"type"`
	TotalItems   int    `json:"totalItems"`
	Items        []any  `json:"items,omitempty"`
	First        string `json:"first,omitempty"`
	Last         string `json:"last,omitempty"`
	Current      string `json:"current,omitempty"`
	OrderedItems []any  `json:"orderedItems,omitempty"`
}

// OrderedCollectionPage represents a page of an ordered collection.
type OrderedCollectionPage struct {
	Context      any    `json:"@context,omitempty"`
	ID           string `json:"id"`
	Type         string `json:"type"`
	Next         string `json:"next,omitempty"`
	Prev         string `json:"prev,omitempty"`
	PartOf       string `json:"partOf,omitempty"`
	OrderedItems []any  `json:"orderedItems"`
}

// WebFingerResponse represents a WebFinger response.
type WebFingerResponse struct {
	Subject string          `json:"subject"`
	Aliases []string        `json:"aliases,omitempty"`
	Links   []WebFingerLink `json:"links"`
}

// WebFingerLink represents a link in WebFinger response.
type WebFingerLink struct {
	Rel  string `json:"rel"`
	Type string `json:"type,omitempty"`
	Href string `json:"href,omitempty"`
}
