package translation

import (
	"context"
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
func runEngines(ctx context.Context, account *models.Account, payload llmsvc.TranslationPayload, source, target string, llmAllowed func() *Error) (*llmsvc.TranslationOutput, string, string, *Error) {
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
			out, reason, err := translateLLM(ctx, provider, account, payload, source, target)
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
			out, err := translateTraditional(svc, payload, source, target)
			if err != nil {
				return nil, "", "traditional_error", err
			}
			if reason := traditionalUnusable(payload, out, source, target); reason != "" {
				log.Warn("Traditional translation returned unusable output (%s, reason %s)", svc.GetProviderName(), reason)
				return out, svc.GetProviderName(), reason, nil
			}
			storeCache(key, contentHash(payload), target, svc.GetProviderName(), out)
			return out, svc.GetProviderName(), "", nil
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
		if ferr == nil {
			return out, name, primary, nil // unusable traditional output, nothing better available
		}
		return nil, "", "", ferr
	}
	second, serr := build(secondary, false)
	if serr != nil {
		log.Warn("Translation fallback %s -> %s unavailable (reason %s, %s)", primary, secondary, reason, serr.Code)
		if ferr == nil {
			return out, name, primary, nil
		}
		return nil, "", "", ferr
	}
	log.Warn("Translation fallback %s -> %s (reason %s)", primary, secondary, reason)
	sout, sname, sreason, sferr := second()
	if sferr != nil || sreason != "" {
		log.Warn("Translation fallback %s -> %s failed (reason %s)", primary, secondary, reason)
		if ferr == nil {
			return out, name, primary, nil
		}
		if sferr != nil && sreason == "" && primary == ModeTraditional {
			return nil, "", "", sferr // e.g. LLM quota exceeded
		}
		return nil, "", "", ferr
	}
	return sout, sname, secondary, nil
}

// traditionalUnusable flags empty output for non-empty input, or output
// identical to the input when the languages are known to differ.
func traditionalUnusable(p llmsvc.TranslationPayload, out *llmsvc.TranslationOutput, source, target string) string {
	in := strings.TrimSpace(htmlsafe.StripTags(p.Content))
	got := strings.TrimSpace(htmlsafe.StripTags(out.Content))
	if in != "" && got == "" {
		return "empty_output"
	}
	src := source
	if src == "" || src == "auto" {
		src = out.DetectedSourceLanguage
	}
	if in != "" && got == in && src != "" && primary(src) != primary(target) && len([]rune(in)) > 3 {
		return "unchanged_output"
	}
	return ""
}
