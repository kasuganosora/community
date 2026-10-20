package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils/translation"
)

type llmTranslationSettings struct {
	Enabled             bool   `json:"enabled"`
	Mode                string `json:"mode"`
	UserDailyLimit      int    `json:"user_daily_limit"`
	DailyLimit          int    `json:"daily_limit"`
	UserPerMinute       int    `json:"user_per_minute"`
	AllowPrivate        bool   `json:"allow_private"`
	TraditionalProvider string `json:"traditional_provider,omitempty"`
}

type llmSettingsResponse struct {
	Providers     []llmsvc.PublicProvider `json:"providers"`
	Services      map[string]string       `json:"services"`
	ProviderTypes []string                `json:"provider_types"`
	ServiceNames  []string                `json:"service_names"`
	Translation   llmTranslationSettings  `json:"translation"`
}

func llmSettingsPayload() llmSettingsResponse {
	cfg := llmsvc.Load()
	out := llmSettingsResponse{Providers: make([]llmsvc.PublicProvider, 0, len(cfg.Providers)), Services: cfg.Services, ProviderTypes: llmsvc.ProviderTypes, ServiceNames: llmsvc.Services}
	for _, p := range cfg.Providers {
		out.Providers = append(out.Providers, p.Public())
	}
	out.Translation = llmTranslationSettings{
		Enabled:             systemsettings.GetBool(systemsettings.KeyIsEnableTranslation, false),
		Mode:                translation.Mode(),
		UserDailyLimit:      systemsettings.GetInt(translation.KeyUserDailyLimit, translation.DefaultUserDailyLimit),
		DailyLimit:          systemsettings.GetInt(translation.KeyDailyLimit, translation.DefaultDailyLimit),
		UserPerMinute:       systemsettings.GetInt(translation.KeyUserPerMinute, translation.DefaultUserPerMinute),
		AllowPrivate:        systemsettings.GetBool(translation.KeyAllowPrivate, false),
		TraditionalProvider: translation.GetService().GetProviderName(),
	}
	return out
}

func llmError(c *gin.Context, status int, code, field string) {
	body := gin.H{"error": code, "error_code": code}
	if field != "" {
		body["field"] = field
	}
	c.JSON(status, body)
}

// GetLLMSettings handles GET /api/v1/admin/settings/external/llm.
// API keys are never returned, only whether one is set and a mask.
func GetLLMSettings(c *gin.Context) {
	c.JSON(http.StatusOK, llmSettingsPayload())
}

type llmProviderInput struct {
	llmsvc.ProviderConfig
	APIKey      *string `json:"api_key"` // write-only; omitted/null keeps the stored key
	ClearAPIKey bool    `json:"clear_api_key"`
}

type llmSettingsInput struct {
	Providers   []llmProviderInput      `json:"providers"`
	Services    map[string]string       `json:"services"`
	Translation *llmTranslationSettings `json:"translation"`
}

// UpdateLLMSettings handles PUT /api/v1/admin/settings/external/llm.
func UpdateLLMSettings(c *gin.Context) {
	var req llmSettingsInput
	if err := c.ShouldBindJSON(&req); err != nil {
		llmError(c, http.StatusUnprocessableEntity, "llm_invalid_request", "")
		return
	}
	current := llmsvc.Load()
	next := llmsvc.Config{Providers: []llmsvc.ProviderConfig{}, Services: map[string]string{}}
	changedKeys := map[string]any{}
	for _, in := range req.Providers {
		p := in.ProviderConfig
		p.Name, p.Model, p.BaseURL = strings.TrimSpace(p.Name), strings.TrimSpace(p.Model), strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
		p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
		p.APIKeyEnc, p.APIKeySalt, p.APIKeyHint = "", "", ""
		if old, ok := current.Find(p.ID); ok && p.ID != "" {
			p.APIKeyEnc, p.APIKeySalt, p.APIKeyHint = old.APIKeyEnc, old.APIKeySalt, old.APIKeyHint
		} else {
			p.ID = llmsvc.NewProviderID()
		}
		switch {
		case in.ClearAPIKey:
			_ = p.SetAPIKey("")
			changedKeys["provider."+p.ID+".api_key"] = "(cleared)"
		case in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "":
			if err := p.SetAPIKey(*in.APIKey); err != nil {
				llmError(c, http.StatusUnprocessableEntity, "llm_invalid_api_key", "api_key")
				return
			}
			changedKeys["provider."+p.ID+".api_key"] = "(changed)"
		}
		next.Providers = append(next.Providers, p)
	}
	// Services may reference providers by id or, for providers created in
	// this request, by their index ("#0").
	for service, ref := range req.Services {
		if strings.HasPrefix(ref, "#") {
			if i, err := strconv.Atoi(ref[1:]); err == nil && i >= 0 && i < len(next.Providers) {
				ref = next.Providers[i].ID
			}
		}
		next.Services[service] = ref
	}
	if err := llmsvc.Validate(next); err != nil {
		var ve *llmsvc.ValidationError
		if errors.As(err, &ve) {
			llmError(c, http.StatusUnprocessableEntity, ve.Code, ve.Field)
			return
		}
		llmError(c, http.StatusUnprocessableEntity, "llm_invalid_request", "")
		return
	}
	var tr *llmTranslationSettings
	if req.Translation != nil {
		tr = req.Translation
		if tr.Mode != translation.ModeLLM && tr.Mode != translation.ModeTraditional {
			llmError(c, http.StatusUnprocessableEntity, "translation_invalid_mode", "translation.mode")
			return
		}
		for field, v := range map[string]int{"translation.user_daily_limit": tr.UserDailyLimit, "translation.daily_limit": tr.DailyLimit, "translation.user_per_minute": tr.UserPerMinute} {
			if v < 0 || v > 1000000 {
				llmError(c, http.StatusUnprocessableEntity, "translation_invalid_limit", field)
				return
			}
		}
		if tr.Mode == translation.ModeLLM && next.Services[llmsvc.ServiceTranslation] == "" {
			llmError(c, http.StatusUnprocessableEntity, "llm_translation_provider_required", "services.translation")
			return
		}
	} else if translation.Mode() == translation.ModeLLM && next.Services[llmsvc.ServiceTranslation] == "" {
		llmError(c, http.StatusUnprocessableEntity, "llm_translation_provider_required", "services.translation")
		return
	}
	extra := map[string]any{}
	if tr != nil {
		extra = map[string]any{
			systemsettings.KeyIsEnableTranslation: tr.Enabled,
			translation.KeyMode:                   tr.Mode,
			translation.KeyUserDailyLimit:         tr.UserDailyLimit,
			translation.KeyDailyLimit:             tr.DailyLimit,
			translation.KeyUserPerMinute:          tr.UserPerMinute,
			translation.KeyAllowPrivate:           tr.AllowPrivate,
		}
		for k, v := range extra {
			changedKeys[k] = v
		}
	}
	if err := llmsvc.Save(next, extra); err != nil {
		log.Warn("llm settings: save failed")
		llmError(c, http.StatusInternalServerError, "llm_save_failed", "")
		return
	}
	changedKeys["llm.providers"] = len(next.Providers)
	changedKeys["llm.services.translation"] = next.Services[llmsvc.ServiceTranslation]
	auditSettingsChange(c, "external.llm", map[string]SettingsField{}, changedKeys, nil)
	c.JSON(http.StatusOK, llmSettingsPayload())
}

// TestLLMProvider handles POST /api/v1/admin/settings/external/llm/test with
// {"id": "<saved provider id>"}. It sends one tiny request; provider error
// texts are reduced to stable codes.
func TestLLMProvider(c *gin.Context) {
	var req struct {
		ID string `json:"id"`
	}
	_ = c.ShouldBindJSON(&req)
	p, ok := llmsvc.Load().Find(req.ID)
	if !ok {
		llmError(c, http.StatusNotFound, "llm_unknown_provider", "id")
		return
	}
	c.JSON(http.StatusOK, llmsvc.Test(c.Request.Context(), p))
}

// LLMUsage handles GET /api/v1/admin/settings/external/llm/usage?days=30.
func LLMUsage(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "30"))
	summary, rows, err := llmsvc.UsageSummary(days)
	if err != nil {
		llmError(c, http.StatusInternalServerError, "llm_usage_unavailable", "")
		return
	}
	if summary == nil {
		summary = []llmsvc.UsageSummaryDay{}
	}
	c.JSON(http.StatusOK, gin.H{"days": summary, "rows": rows, "today": gin.H{
		"day":               llmsvc.Today(),
		"translation_calls": llmsvc.CallsToday(llmsvc.FeatureTranslation),
	}})
}
