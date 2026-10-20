package translation

import (
	"context"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// KeyGroupMessages allows translating group chat (Matrix room) messages.
//
// Policy: group messages are not public posts. When unset, the setting
// defaults to on only when the primary engine is a traditional backend at an
// explicitly configured (self-hosted, LibreTranslate-compatible) URL, so the
// text stays on-premises; otherwise it defaults to off. Independently, any
// request that would send a message to an LLM (primary or fallback) requires
// config.translations.llm_allow_private unless the room is public (public
// join rule or world-readable history); otherwise that engine is refused with
// translation_visibility_restricted.
const KeyGroupMessages = "config.translations.group_messages"

// GroupMessagesDefault is the effective default of KeyGroupMessages.
func GroupMessagesDefault() bool {
	if Mode() != ModeTraditional {
		return false
	}
	_, ok := FallbackService()
	return ok
}

// GroupMessagesEnabled reports whether group message translation is offered.
func GroupMessagesEnabled() bool {
	return Enabled() && GroupMessagesSetting()
}

// GroupMessagesSetting is the stored KeyGroupMessages value or its default.
func GroupMessagesSetting() bool { return boolSetting(KeyGroupMessages, GroupMessagesDefault()) }

// GroupLLMAllowed reports whether a message of a room may be sent to an LLM.
func GroupLLMAllowed(publicRoom bool) bool {
	return publicRoom || systemsettings.GetBool(KeyAllowPrivate, false)
}

// GroupTranslatable reports whether members of a room should be offered a
// Translate action: the feature is on and at least one engine may be used.
func GroupTranslatable(publicRoom bool) bool {
	if !GroupMessagesEnabled() {
		return false
	}
	if Mode() == ModeTraditional || GroupLLMAllowed(publicRoom) {
		return true
	}
	_, ok := FallbackService()
	return ok && FallbackEnabled()
}

// ErrGroupVisibilityRestricted uses the post code with a group-specific text.
var ErrGroupVisibilityRestricted = newErr(403, "translation_visibility_restricted", "Only messages of public groups can be translated on this server")

// GroupMessageTranslation is the response for a translated group message.
type GroupMessageTranslation struct {
	Content                string `json:"content"`
	DetectedSourceLanguage string `json:"detected_source_language"`
	Language               string `json:"language"`
	Provider               string `json:"provider"`
	Engine                 string `json:"engine"`
}

// TranslateGroupMessage translates the (sanitised HTML) content of a group
// chat event. The caller has already checked room membership and that the
// user may read the event.
func TranslateGroupMessage(ctx context.Context, account *models.Account, eventID, contentHTML, source, target string, publicRoom bool) (*GroupMessageTranslation, *Error) {
	if !GroupMessagesEnabled() {
		return nil, ErrDisabled
	}
	if account == nil {
		return nil, ErrLoginRequired
	}
	target, ok := NormalizeTarget(target)
	if !ok {
		return nil, ErrInvalidLanguage
	}
	payload := llmsvc.TranslationPayload{Content: htmlsafe.Sanitize(contentHTML)}
	if strings.TrimSpace(htmlsafe.StripTags(payload.Content)) == "" {
		return nil, ErrNothingToTranslate
	}
	if source != "" && primary(source) == primary(target) && !strings.HasPrefix(strings.ToLower(target), "zh-") {
		return nil, ErrSameLanguage
	}
	hash := contentHash(payload)
	// An edit changes the hash: drop translations of earlier versions.
	PurgeGroupEvent(eventID, hash)
	llmAllowed := func() *Error {
		if !GroupLLMAllowed(publicRoom) {
			return ErrGroupVisibilityRestricted
		}
		return nil
	}
	out, provider, engine, err := runEngines(ctx, account, payload, source, "", target, llmAllowed)
	if err != nil {
		if err == ErrVisibilityRestricted {
			err = ErrGroupVisibilityRestricted
		}
		return nil, err
	}
	rememberGroupHash(eventID, hash)
	res := &GroupMessageTranslation{Content: out.Content, DetectedSourceLanguage: out.DetectedSourceLanguage, Language: target, Provider: provider, Engine: engine}
	if res.DetectedSourceLanguage == "" {
		res.DetectedSourceLanguage = source
	}
	return res, nil
}

func rememberGroupHash(eventID, hash string) {
	conn := db.GetDB()
	if conn == nil || eventID == "" {
		return
	}
	var n int64
	if conn.Model(&llmsvc.GroupTranslationCacheRef{}).Where("event_id = ? AND content_hash = ?", eventID, hash).Count(&n).Error != nil || n > 0 {
		return
	}
	_ = conn.Create(&llmsvc.GroupTranslationCacheRef{EventID: eventID, ContentHash: hash, CreatedAt: time.Now()}).Error
}

// PurgeGroupEvent removes cached translations of a group event's content
// except keepHash (pass "" on redaction). Entries are shared by content, so a
// message with identical text elsewhere simply translates again.
func PurgeGroupEvent(eventID, keepHash string) {
	conn := db.GetDB()
	if conn == nil || eventID == "" {
		return
	}
	var hashes []string
	if err := conn.Model(&llmsvc.GroupTranslationCacheRef{}).Where("event_id = ?", eventID).Pluck("content_hash", &hashes).Error; err != nil {
		return
	}
	for _, h := range hashes {
		if h == keepHash {
			continue
		}
		if err := conn.Where("content_hash = ?", h).Delete(&llmsvc.TranslationCache{}).Error; err != nil {
			log.Warn("translation cache: group purge failed")
			continue
		}
		conn.Where("event_id = ? AND content_hash = ?", eventID, h).Delete(&llmsvc.GroupTranslationCacheRef{})
	}
}

// PurgeGroupEventAsync purges off the caller's (transaction) path.
func PurgeGroupEventAsync(eventID string) { purgeAsync(func() { PurgeGroupEvent(eventID, "") }) }
