package status

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils/translation"
	"github.com/kasuganosora/thinkbot/llm"
	"github.com/stretchr/testify/require"
)

// fakeLLM "translates" by upper-casing text outside placeholders and tags.
type fakeLLM struct{ calls atomic.Int32 }

func (f *fakeLLM) Name() string { return "fake" }
func (f *fakeLLM) DoStream(context.Context, llm.GenerateParams) (*llm.StreamResult, error) {
	return nil, context.Canceled
}
func (f *fakeLLM) DoGenerate(_ context.Context, p llm.GenerateParams) (*llm.GenerateResult, error) {
	f.calls.Add(1)
	var in map[string]any
	if err := json.Unmarshal([]byte(llm.TextFromParts(p.Messages[0].Content)), &in); err != nil {
		return nil, err
	}
	in["content"] = strings.Replace(in["content"].(string), "Hello world", "こんにちは世界", 1)
	if s, ok := in["spoiler_text"].(string); ok {
		in["spoiler_text"] = "注意 " + s
	}
	if opts, ok := in["poll_options"].([]any); ok {
		for i := range opts {
			opts[i] = "選択" + strconv.Itoa(i)
		}
	}
	in["detected_source_language"] = "en"
	out, _ := json.Marshal(in)
	return &llm.GenerateResult{Text: "```json\n" + string(out) + "\n```", Usage: llm.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150}}, nil
}

func setTranslationSettings(t *testing.T, values map[string]any) {
	t.Helper()
	for k, v := range values {
		old := systemsettings.Get(k, nil)
		systemsettings.Set(k, v)
		t.Cleanup(func() { systemsettings.Set(k, old) })
	}
}

func TestTranslate_LLMModePreservesEntitiesCachesAndLimits(t *testing.T) {
	ts := newTestSetup(t)
	require.NoError(t, ts.db.AutoMigrate(llmsvc.Models()...))
	translation.ResetLimiterForTest()
	fake := &fakeLLM{}
	llmsvc.SetTestProviderFactory(func(llmsvc.ProviderConfig) (llm.Provider, error) { return fake, nil })
	t.Cleanup(func() { llmsvc.SetTestProviderFactory(nil) })

	provider := llmsvc.ProviderConfig{ID: "fake1", Name: "Example LLM", Type: llmsvc.TypeOpenAI, Model: "example-model", InputPricePer1M: 1, OutputPricePer1M: 2, Currency: "USD"}
	require.NoError(t, provider.SetAPIKey("sk-example-test"))
	cfg, _ := json.Marshal(llmsvc.Config{Providers: []llmsvc.ProviderConfig{provider}, Services: map[string]string{llmsvc.ServiceTranslation: "fake1"}})
	setTranslationSettings(t, map[string]any{
		llmsvc.KeyConfig:                      string(cfg),
		systemsettings.KeyIsEnableTranslation: true,
		translation.KeyMode:                   translation.ModeLLM,
		translation.KeyUserDailyLimit:         2,
		translation.KeyDailyLimit:             100,
		translation.KeyUserPerMinute:          10,
	})

	html := `<p>Hello world <a href="https://social.example/tags/Example" class="mention hashtag">#<span>Example</span></a> :blobcat:</p>`
	mk := func() *models.Status {
		return ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
			s.Language = "en"
			s.Content = models.StatusContent{ContentFormat: "html", Content: html, SpoilerText: "spoilers"}
		})
	}
	call := func(s *models.Status, lang string) (int, map[string]any) {
		c, w := newGinContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(s.Id, 10)}, {Key: "lang", Value: lang}})
		setAccount(c, ts.acct)
		safeHandler(c, Translate)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}

	first := mk()
	code, body := call(first, "ja")
	require.Equal(t, 200, code, body)
	require.Contains(t, body["content"], "こんにちは世界")
	require.Contains(t, body["content"], `href="https://social.example/tags/Example"`)
	require.Contains(t, body["content"], ":blobcat:")
	require.Equal(t, "注意 spoilers", body["spoiler_text"])
	require.Equal(t, "Example LLM", body["provider"])
	require.Equal(t, "en", body["detected_source_language"])

	// Identical content on another status is served from the shared cache.
	code, _ = call(mk(), "ja")
	require.Equal(t, 200, code)
	require.EqualValues(t, 1, fake.calls.Load())

	// Usage and cost were recorded through ThinkBot's stats recorder.
	var usage llmsvc.UsageDaily
	require.NoError(t, ts.db.Where("feature = ?", llmsvc.FeatureTranslation).First(&usage).Error)
	require.EqualValues(t, 1, usage.Calls)
	require.EqualValues(t, 150, usage.InputTokens+usage.OutputTokens)
	require.InDelta(t, 0.0002, usage.Cost, 1e-9)

	// Per-user daily limit (2): second real call ok, third refused.
	code, _ = call(first, "fr")
	require.Equal(t, 200, code)
	code, body = call(first, "de")
	require.Equal(t, 429, code)
	require.Equal(t, "translation_user_quota_exceeded", body["error_code"])

	// Same language is refused with a stable code.
	code, body = call(first, "en")
	require.Equal(t, 422, code)
	require.Equal(t, "translation_same_language", body["error_code"])
}

func TestTranslate_LLMRequiresLogin(t *testing.T) {
	ts := newTestSetup(t)
	require.NoError(t, ts.db.AutoMigrate(llmsvc.Models()...))
	provider := llmsvc.ProviderConfig{ID: "fake2", Name: "Example", Type: llmsvc.TypeOpenAI, Model: "m"}
	cfg, _ := json.Marshal(llmsvc.Config{Providers: []llmsvc.ProviderConfig{provider}, Services: map[string]string{llmsvc.ServiceTranslation: "fake2"}})
	setTranslationSettings(t, map[string]any{llmsvc.KeyConfig: string(cfg), systemsettings.KeyIsEnableTranslation: true, translation.KeyMode: translation.ModeLLM})
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(ts.status.Id, 10)}, {Key: "lang", Value: "ja"}})
	safeHandler(c, Translate)
	require.Contains(t, []int{401, 422}, w.Code, w.Body.String())
}
