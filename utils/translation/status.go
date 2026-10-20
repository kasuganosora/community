package translation

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/kasuganosora/community/reqid"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm/clause"
)

// Setting keys of the translation service (database-backed).
const (
	KeyMode           = "config.translations.mode"                 // "traditional" | "llm"
	KeyUserDailyLimit = "config.translations.llm_user_daily_limit" // LLM calls per user per UTC day, 0 = unlimited
	KeyDailyLimit     = "config.translations.llm_daily_limit"      // LLM calls per UTC day for the instance, 0 = unlimited
	KeyUserPerMinute  = "config.translations.llm_user_per_minute"  // burst limit per user, 0 = unlimited
	KeyAllowPrivate   = "config.translations.llm_allow_private"    // allow LLM translation of followers-only/direct posts

	ModeTraditional = "traditional"
	ModeLLM         = "llm"

	DefaultUserDailyLimit = 50
	DefaultDailyLimit     = 1000
	DefaultUserPerMinute  = 10
)

// Mode returns the configured translation mode.
func Mode() string {
	if systemsettings.GetString(KeyMode, ModeTraditional) == ModeLLM {
		return ModeLLM
	}
	return ModeTraditional
}

// Enabled reports whether translation is switched on and has a usable backend.
// PublicOnly reports whether translation is limited to public and unlisted
// posts (LLM mode without the "allow private posts" admin toggle).
func PublicOnly() bool {
	return Mode() == ModeLLM && !systemsettings.GetBool(KeyAllowPrivate, false)
}

func visibilityAllowed(s *models.Status) bool {
	return s.Visibility == models.StatusVisibilityPublic || s.Visibility == models.StatusVisibilityUnlisted
}

func Enabled() bool {
	if !systemsettings.GetBool(systemsettings.KeyIsEnableTranslation, false) {
		return false
	}
	if Mode() == ModeLLM {
		_, ok := llmsvc.ServiceProvider(llmsvc.ServiceTranslation)
		return ok
	}
	return true
}

// Error is a translation failure with a stable code. Message is the English
// text returned as "error" for Mastodon clients; the web UI localises Code.
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code }

func newErr(status int, code, msg string) *Error {
	return &Error{Status: status, Code: code, Message: msg}
}

// Stable error codes.
var (
	ErrDisabled             = newErr(403, "translation_disabled", "Translation is not enabled on this server")
	ErrLoginRequired        = newErr(401, "translation_login_required", "Translation requires a signed-in account")
	ErrInvalidLanguage      = newErr(422, "translation_invalid_language", "Invalid target language")
	ErrNothingToTranslate   = newErr(422, "translation_nothing_to_translate", "This post has no text to translate")
	ErrSameLanguage         = newErr(422, "translation_same_language", "This post is already in the target language")
	ErrRateLimited          = newErr(429, "translation_rate_limited", "Too many translation requests, please try again later")
	ErrUserQuota            = newErr(429, "translation_user_quota_exceeded", "You have reached your daily translation limit")
	ErrDailyQuota           = newErr(429, "translation_daily_quota_exceeded", "This server has reached its daily translation limit")
	ErrUnavailable          = newErr(503, "translation_unavailable", "The translation service is unavailable")
	ErrVisibilityRestricted = newErr(403, "translation_visibility_restricted", "Only public and unlisted posts can be translated on this server")
	ErrFailed               = newErr(502, "translation_failed", "The translation service could not translate this post")
	// ErrUnusable: the traditional backend answered, but with text that is
	// not a translation (echoed input, wrong script).
	ErrUnusable = newErr(422, "translation_failed", "The translation service could not translate this post")
	// ErrLanguageUnsupported: the backend has no model for this language
	// pair (not an outage).
	ErrLanguageUnsupported = newErr(422, "translation_language_unsupported", "Translation from this language is not supported yet")

	// LLM mode without an explicit traditional fallback: the same stable
	// codes, but never a 5xx (the server itself is healthy).
	ErrLLMFailed      = newErr(422, "translation_failed", "The translation service could not translate this post")
	ErrLLMUnavailable = newErr(424, "translation_unavailable", "The translation service is unavailable")
)

// Engines reported in StatusTranslation.Engine.
const (
	EngineLLM         = "llm"
	EngineTraditional = "traditional"
)

// PollTranslation / MediaTranslation follow Mastodon's Translation entity.
type PollTranslation struct {
	ID      string                  `json:"id"`
	Options []PollOptionTranslation `json:"options"`
}
type PollOptionTranslation struct {
	Title string `json:"title"`
}
type MediaTranslation struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// StatusTranslation is the Mastodon Translation entity (plus "language").
type StatusTranslation struct {
	Content                string             `json:"content"`
	SpoilerText            string             `json:"spoiler_text"`
	Poll                   *PollTranslation   `json:"poll,omitempty"`
	MediaAttachments       []MediaTranslation `json:"media_attachments"`
	DetectedSourceLanguage string             `json:"detected_source_language"`
	Language               string             `json:"language"`
	Provider               string             `json:"provider"`
	// Engine is "llm" or "traditional" (extra field, ignored by Mastodon clients).
	Engine string `json:"engine"`
}

var langPattern = func(s string) bool {
	if len(s) < 2 || len(s) > 16 {
		return false
	}
	for i, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || (i > 1 && (r == '-' || r >= '0' && r <= '9'))) {
			return false
		}
	}
	return true
}

// NormalizeTarget validates a BCP 47-ish language code.
func NormalizeTarget(lang string) (string, bool) {
	lang = strings.TrimSpace(strings.ReplaceAll(lang, "_", "-"))
	if !langPattern(lang) {
		return "", false
	}
	parts := strings.SplitN(lang, "-", 2)
	out := strings.ToLower(parts[0])
	if len(parts) == 2 {
		out += "-" + strings.ToUpper(parts[1])
	}
	return out, true
}

func primary(lang string) string {
	return strings.ToLower(strings.SplitN(strings.ReplaceAll(lang, "_", "-"), "-", 2)[0])
}

// payloadOf extracts the translatable parts of a status.
func payloadOf(s *models.Status) (llmsvc.TranslationPayload, []string, *models.Poll) {
	var p llmsvc.TranslationPayload
	p.Content = s.ContentHTML(htmlsafe.OptionsForBaseURL(systemsettings.GetBaseURL()))
	p.SpoilerText = s.SpoilerTextPlain()
	var poll *models.Poll
	if s.Poll != nil && s.Poll.Id != 0 {
		poll = s.Poll
		for _, o := range s.Poll.Options {
			if o != nil {
				p.PollOptions = append(p.PollOptions, o.Content)
			}
		}
	}
	var mediaIDs []string
	for _, a := range s.Attachments {
		if a != nil && strings.TrimSpace(a.Description) != "" {
			mediaIDs = append(mediaIDs, strconv.FormatUint(a.Id, 10))
			p.MediaDescriptions = append(p.MediaDescriptions, a.Description)
		}
	}
	return p, mediaIDs, poll
}

func cacheKey(backend, target string, p llmsvc.TranslationPayload) string {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256([]byte(backend + "\x00" + target + "\x00" + string(raw)))
	return hex.EncodeToString(sum[:])
}

type minuteLimiter struct {
	mu   sync.Mutex
	hits map[uint64][]time.Time
}

var burst = &minuteLimiter{hits: map[uint64][]time.Time{}}

func (l *minuteLimiter) allow(account uint64, limit int) bool {
	if limit <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.hits[account][:0]
	for _, t := range l.hits[account] {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		l.hits[account] = kept
		return false
	}
	l.hits[account] = append(kept, now)
	if len(l.hits) > 10000 { // drop idle entries
		for k, v := range l.hits {
			if len(v) == 0 || now.Sub(v[len(v)-1]) >= time.Minute {
				delete(l.hits, k)
			}
		}
	}
	return true
}

// ResetLimiterForTest clears the burst limiter.
func ResetLimiterForTest() { burst = &minuteLimiter{hits: map[uint64][]time.Time{}} }

// TranslateStatus translates a status into target using the configured backend.
func TranslateStatus(ctx context.Context, s *models.Status, account *models.Account, target string) (*StatusTranslation, *Error) {
	if !Enabled() {
		return nil, ErrDisabled
	}
	target, ok := NormalizeTarget(target)
	if !ok {
		return nil, ErrInvalidLanguage
	}
	payload, mediaIDs, poll := payloadOf(s)
	if strings.TrimSpace(htmlsafe.StripTags(payload.Content)) == "" && payload.SpoilerText == "" && len(payload.PollOptions) == 0 && len(payload.MediaDescriptions) == 0 {
		return nil, ErrNothingToTranslate
	}
	if s.Language != "" && primary(s.Language) == primary(target) && !strings.HasPrefix(strings.ToLower(target), "zh-") {
		return nil, ErrSameLanguage
	}

	llmAllowed := func() *Error {
		if !systemsettings.GetBool(KeyAllowPrivate, false) && !visibilityAllowed(s) {
			return ErrVisibilityRestricted
		}
		return nil
	}
	// A remote server's language tag is untrusted (a client default often
	// mislabels posts): let the backend detect the source and pass the tag
	// only as a hint for when detection fails.
	source, hint := s.Language, ""
	if !s.IsLocal() {
		source, hint = "auto", s.Language
	}
	out, providerName, engine, terr := runEngines(ctx, account, payload, source, hint, target, llmAllowed)
	if terr != nil {
		return nil, terr
	}
	if s.Id != 0 {
		rememberStatusHash(s.Id, contentHash(payload))
	}

	res := &StatusTranslation{
		Content: out.Content, SpoilerText: out.SpoilerText, Language: target, Provider: providerName, Engine: engine,
		DetectedSourceLanguage: out.DetectedSourceLanguage, MediaAttachments: []MediaTranslation{},
	}
	if res.DetectedSourceLanguage == "" {
		res.DetectedSourceLanguage = source
	}
	if res.DetectedSourceLanguage == "auto" {
		res.DetectedSourceLanguage = "und" // remote tag unconfirmed; do not label with it
	}
	if poll != nil && len(out.PollOptions) == len(payload.PollOptions) {
		res.Poll = &PollTranslation{ID: strconv.FormatUint(poll.Id, 10)}
		for _, o := range out.PollOptions {
			res.Poll.Options = append(res.Poll.Options, PollOptionTranslation{Title: o})
		}
	}
	if len(out.MediaDescriptions) == len(mediaIDs) {
		for i, d := range out.MediaDescriptions {
			res.MediaAttachments = append(res.MediaAttachments, MediaTranslation{ID: mediaIDs[i], Description: d})
		}
	}
	return res, nil
}

// translateLLM returns a non-empty fallback reason when the failure may be
// served by the traditional backend (unusable output, provider/network error);
// limit errors carry no reason.
func translateLLM(ctx context.Context, p llmsvc.ProviderConfig, account *models.Account, payload llmsvc.TranslationPayload, source, target string) (*llmsvc.TranslationOutput, string, *Error) {
	if !burst.allow(account.Id, systemsettings.GetInt(KeyUserPerMinute, DefaultUserPerMinute)) {
		return nil, "", ErrRateLimited
	}
	if limit := systemsettings.GetInt64(KeyUserDailyLimit, DefaultUserDailyLimit); limit > 0 && llmsvc.UserCallsToday(llmsvc.FeatureTranslation, account.Id) >= limit {
		return nil, "", ErrUserQuota
	}
	if limit := systemsettings.GetInt64(KeyDailyLimit, DefaultDailyLimit); limit > 0 && llmsvc.CallsToday(llmsvc.FeatureTranslation) >= limit {
		return nil, "", ErrDailyQuota
	}
	llmsvc.IncrementUserCalls(llmsvc.FeatureTranslation, account.Id)
	out, err := llmsvc.TranslateWithLLM(ctx, p, payload, source, target)
	if err != nil {
		if reason := llmsvc.OutputReason(err); reason != "" {
			log.Warn("LLM translation returned unusable output (provider %s, reason %s, request_id %s)", p.ID, reason, reqid.From(ctx))
			return nil, reason, ErrLLMFailed
		}
		reason := llmsvc.ErrorCode(err)
		log.Warn("LLM translation failed (provider %s, reason %s, request_id %s)", p.ID, reason, reqid.From(ctx))
		return nil, reason, ErrLLMUnavailable
	}
	out.Content = htmlsafe.Sanitize(out.Content)
	out.SpoilerText = htmlsafe.StripTags(out.SpoilerText)
	for i := range out.PollOptions {
		out.PollOptions[i] = htmlsafe.StripTags(out.PollOptions[i])
	}
	for i := range out.MediaDescriptions {
		out.MediaDescriptions[i] = htmlsafe.StripTags(out.MediaDescriptions[i])
	}
	return out, "", nil
}

// translateTraditional returns the output and the name of the engine that
// produced it: the backend's own report (mt-gateway: "hy-mt",
// "libretranslate"; "mixed" when fields differ) or the provider name.
func translateTraditional(ctx context.Context, service *Service, p llmsvc.TranslationPayload, source, sourceHint, target string) (*llmsvc.TranslationOutput, string, *Error) {
	if source == "" {
		source = "auto"
	}
	ctx = WithSourceHint(ctx, sourceHint)
	engine := ""
	one := func(text string) (string, string, error) {
		if strings.TrimSpace(text) == "" {
			return text, "", nil
		}
		r, err := service.TranslateContext(ctx, text, source, target)
		if err != nil {
			return "", "", err
		}
		switch {
		case r.Engine == "":
		case engine == "":
			engine = r.Engine
		case engine != r.Engine:
			engine = "mixed"
		}
		return r.Text, r.DetectedSource, nil
	}
	out := &llmsvc.TranslationOutput{}
	var err error
	if out.Content, out.DetectedSourceLanguage, err = one(p.Content); err != nil {
		log.Warn("Translation failed (%s)", service.GetProviderName())
		return nil, "", backendErr(err)
	}
	out.Content = htmlsafe.Sanitize(out.Content)
	var spoilerLang string
	if out.SpoilerText, spoilerLang, err = one(p.SpoilerText); err != nil {
		return nil, "", backendErr(err)
	}
	if out.DetectedSourceLanguage == "" || out.DetectedSourceLanguage == "auto" {
		out.DetectedSourceLanguage = spoilerLang
	}
	for _, o := range p.PollOptions {
		t, _, err := one(o)
		if err != nil {
			return nil, "", backendErr(err)
		}
		out.PollOptions = append(out.PollOptions, htmlsafe.StripTags(t))
	}
	for _, d := range p.MediaDescriptions {
		t, _, err := one(d)
		if err != nil {
			return nil, "", backendErr(err)
		}
		out.MediaDescriptions = append(out.MediaDescriptions, htmlsafe.StripTags(t))
	}
	if out.DetectedSourceLanguage == "auto" {
		out.DetectedSourceLanguage = ""
	}
	if engine == "" {
		engine = service.GetProviderName()
	}
	return out, engine, nil
}

// backendErr maps a backend failure: an unsupported language pair or an
// echo the backend refused to return are 422s; anything else is an outage.
func backendErr(err error) *Error {
	var be *BackendError
	if errors.As(err, &be) {
		switch be.Code {
		case backendCodeLanguageUnsupported:
			return ErrLanguageUnsupported
		case backendCodeUnchanged:
			return ErrUnusable
		}
	}
	return ErrUnavailable
}

type cachedResult struct {
	llmsvc.TranslationOutput
	Provider string `json:"provider"`
}

const cacheTTL = 30 * 24 * time.Hour

func lookupCache(key string) (*llmsvc.TranslationOutput, string) {
	conn := db.GetDB()
	if conn == nil {
		return nil, ""
	}
	var row llmsvc.TranslationCache
	if err := conn.Where("cache_key = ?", key).Limit(1).Find(&row).Error; err != nil || row.ID == 0 {
		return nil, ""
	}
	if time.Since(row.CreatedAt) > cacheTTL {
		return nil, ""
	}
	raw, err := base64.StdEncoding.DecodeString(row.Result)
	if err != nil {
		return nil, ""
	}
	var c cachedResult
	if json.Unmarshal(raw, &c) != nil {
		return nil, ""
	}
	return &c.TranslationOutput, row.Provider
}

func storeCache(key, hash, target, provider string, out *llmsvc.TranslationOutput) {
	conn := db.GetDB()
	if conn == nil {
		return
	}
	raw, err := json.Marshal(cachedResult{TranslationOutput: *out, Provider: provider})
	if err != nil {
		return
	}
	// Replace an expired row, keep a concurrent fresh one.
	conn.Where("cache_key = ? AND created_at < ?", key, time.Now().Add(-cacheTTL)).Delete(&llmsvc.TranslationCache{})
	row := llmsvc.TranslationCache{CacheKey: key, ContentHash: hash, TargetLang: target, Provider: provider, Result: base64.StdEncoding.EncodeToString(raw), CreatedAt: time.Now()}
	if err := conn.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		log.Warn("translation cache: could not store entry")
	}
	if rand.IntN(200) == 0 {
		PruneExpiredCache()
	}
}

// SupportedLanguages lists the target languages offered to clients.
var SupportedLanguages = []string{"en", "zh-CN", "zh-TW", "ja", "ko", "ru", "fr", "de", "es", "pt", "it"}

// LanguagePairs returns Mastodon's translation_languages map (source -> targets).
func LanguagePairs() map[string][]string {
	out := map[string][]string{}
	if !Enabled() {
		return out
	}
	// Sources are advisory for Mastodon clients: the backend detects the
	// language itself, so list widely used ones beyond the target set.
	sources := []string{"en", "zh", "ja", "ko", "ru", "fr", "de", "es", "pt", "it", "no", "nb", "nn", "nl", "sv", "da", "fi", "pl", "cs", "uk", "tr", "id", "vi", "ar"}
	for _, src := range sources {
		targets := make([]string, 0, len(SupportedLanguages))
		for _, t := range SupportedLanguages {
			if primary(t) != src || src == "zh" {
				targets = append(targets, t)
			}
		}
		out[src] = targets
	}
	return out
}
