package translation

import (
	"context"
	"github.com/kasuganosora/community/reqid"
	"strings"

	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
)

// KeyFallback enables the secondary engine (the one that is not the primary
// KeyMode). Stored in the database like every other translation policy; .env
// only carries connection settings (LIBRETRANSLATE_URL, API key).
const KeyFallback = "config.translations.fallback_enabled"

// FallbackEnabled reports whether the secondary engine may be tried. When
// unset it defaults to on for an LLM primary (the historical behaviour: LLM
// falls back to an explicit self-hosted traditional backend) and off for a
// traditional primary, so content is never sent to an LLM without an admin
// choosing it.
func FallbackEnabled() bool {
	return boolSetting(KeyFallback, Mode() == ModeLLM)
}

// boolSetting treats a missing or null value as unset (use def).
func boolSetting(key string, def bool) bool {
	if systemsettings.Get(key, nil) == nil {
		return def
	}
	return systemsettings.GetBool(key, def)
}

var traditionalOverride func() *Service

// SetTraditionalServiceForTest replaces the primary traditional service (nil restores it).
func SetTraditionalServiceForTest(f func() *Service) { traditionalOverride = f }

func primaryTraditional() *Service {
	if traditionalOverride != nil {
		return traditionalOverride()
	}
	return GetService()
}

// engineStep is one engine attempt. fallbackReason is non-empty when the
// failure may be served by the other engine.
type engineStep func() (out *llmsvc.TranslationOutput, provider, fallbackReason string, err *Error)

// runEngines translates payload with the primary engine and, when enabled,
// the other one. llmAllowed applies privacy policy before any content is sent
// to an LLM, whether it is the primary engine or the fallback.
func runEngines(ctx context.Context, account *models.Account, payload llmsvc.TranslationPayload, source, hint, target string, llmAllowed func() *Error) (*llmsvc.TranslationOutput, string, string, *Error) {
	ctx, rid := reqid.Ensure(ctx)
	primary := Mode()
	llmStep := func(asPrimary bool) (engineStep, *Error) {
		if account == nil {
			return nil, ErrLoginRequired
		}
		if llmAllowed != nil {
			if err := llmAllowed(); err != nil {
				return nil, err
			}
		}
		provider, ok := llmsvc.ServiceProvider(llmsvc.ServiceTranslation)
		if !ok {
			return nil, ErrDisabled
		}
		key := cacheKey("llm:"+provider.ID+":"+provider.Type+":"+provider.Model, target, payload)
		return func() (*llmsvc.TranslationOutput, string, string, *Error) {
			if cached, name := lookupCache(key); cached != nil {
				return cached, name, "", nil
			}
			llmSource := source
			if llmSource == "auto" {
				llmSource = "" // the model detects the language itself
			}
			out, reason, err := translateLLM(ctx, provider, account, payload, llmSource, target)
			if err != nil {
				return nil, "", reason, err
			}
			storeCache(key, contentHash(payload), target, provider.Name, out)
			return out, provider.Name, "", nil
		}, nil
	}
	tradStep := func(asPrimary bool) (engineStep, *Error) {
		svc := primaryTraditional()
		if !asPrimary {
			fb, ok := FallbackService()
			if !ok {
				return nil, ErrUnavailable
			}
			svc = fb
		}
		key := cacheKey("traditional:"+svc.GetProviderName(), target, payload)
		return func() (*llmsvc.TranslationOutput, string, string, *Error) {
			if cached, name := lookupCache(key); cached != nil {
				return cached, name, "", nil
			}
			out, name, err := translateTraditional(ctx, svc, payload, source, hint, target)
			if err != nil {
				log.Warn("Traditional translation failed (%s, %s, request_id %s)", svc.GetProviderName(), err.Code, rid)
				reason := "traditional_error"
				switch err {
				case ErrLanguageUnsupported:
					reason = "language_unsupported"
				case ErrUnusable:
					reason = "unchanged_output"
				}
				return nil, "", reason, err
			}
			if reason := traditionalUnusable(payload, out, source, target); reason != "" {
				log.Warn("Traditional translation returned unusable output (%s, reason %s, request_id %s)", svc.GetProviderName(), reason, rid)
				return nil, "", reason, ErrUnusable
			}
			storeCache(key, contentHash(payload), target, name, out)
			return out, name, "", nil
		}, nil
	}
	build := func(engine string, asPrimary bool) (engineStep, *Error) {
		if engine == ModeLLM {
			return llmStep(asPrimary)
		}
		return tradStep(asPrimary)
	}
	secondary := ModeLLM
	if primary == ModeLLM {
		secondary = ModeTraditional
	}
	first, err := build(primary, true)
	if err != nil {
		// A policy refusal of the primary LLM may still be served by an
		// enabled traditional fallback, which keeps content on-premises.
		if primary == ModeLLM && err.Code == ErrVisibilityRestricted.Code && FallbackEnabled() {
			if second, serr := build(secondary, false); serr == nil {
				if out, name, reason, e := second(); e == nil && reason == "" {
					return out, name, EngineTraditional, nil
				}
			}
		}
		return nil, "", "", err
	}
	out, name, reason, ferr := first()
	if reason == "" {
		if ferr != nil {
			return nil, "", "", ferr
		}
		return out, name, primary, nil
	}
	// Primary failed (or produced unusable output) with a fallback-eligible reason.
	if !FallbackEnabled() {
		return nil, "", "", ferr
	}
	second, serr := build(secondary, false)
	if serr != nil {
		log.Warn("Translation fallback %s -> %s unavailable (reason %s, %s, request_id %s)", primary, secondary, reason, serr.Code, rid)
		return nil, "", "", ferr
	}
	log.Warn("Translation fallback %s -> %s (reason %s, request_id %s)", primary, secondary, reason, rid)
	sout, sname, sreason, sferr := second()
	if sferr != nil || sreason != "" {
		log.Warn("Translation fallback %s -> %s failed (reason %s, request_id %s)", primary, secondary, reason, rid)
		if sferr != nil && sreason == "" && primary == ModeTraditional {
			return nil, "", "", sferr // e.g. LLM quota exceeded
		}
		return nil, "", "", ferr
	}
	return sout, sname, secondary, nil
}

// traditionalUnusable flags output that is not a translation: empty for
// non-empty input, the input echoed back (ignoring whitespace, punctuation
// width, mentions, URLs, hashtags and emoji), or text not in the target's
// script. Such output is never shown or cached.
func traditionalUnusable(p llmsvc.TranslationPayload, out *llmsvc.TranslationOutput, source, target string) string {
	src := source
	if src == "" || src == "auto" {
		src = out.DetectedSourceLanguage
	}
	in, got := htmlsafe.StripTags(p.Content), htmlsafe.StripTags(out.Content)
	if strings.TrimSpace(in) == "" {
		in, got = p.SpoilerText, out.SpoilerText
	}
	return unusableText(in, got, src, target)
}
