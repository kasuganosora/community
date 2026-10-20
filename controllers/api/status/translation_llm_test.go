package status

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func setupFakeLLM(t *testing.T, extra map[string]any) (*testSetup, *fakeLLM) {
	t.Helper()
	ts := newTestSetup(t)
	require.NoError(t, ts.db.AutoMigrate(llmsvc.Models()...))
	translation.ResetLimiterForTest()
	translation.SetSyncPurgeForTest()
	fake := &fakeLLM{}
	llmsvc.SetTestProviderFactory(func(llmsvc.ProviderConfig) (llm.Provider, error) { return fake, nil })
	t.Cleanup(func() { llmsvc.SetTestProviderFactory(nil) })
	provider := llmsvc.ProviderConfig{ID: "fake3", Name: "Example LLM", Type: llmsvc.TypeOpenAI, Model: "example-model"}
	cfg, _ := json.Marshal(llmsvc.Config{Providers: []llmsvc.ProviderConfig{provider}, Services: map[string]string{llmsvc.ServiceTranslation: "fake3"}})
	values := map[string]any{
		llmsvc.KeyConfig: string(cfg), systemsettings.KeyIsEnableTranslation: true, translation.KeyMode: translation.ModeLLM,
		translation.KeyUserDailyLimit: 0, translation.KeyDailyLimit: 0, translation.KeyUserPerMinute: 0,
	}
	for k, v := range extra {
		values[k] = v
	}
	setTranslationSettings(t, values)
	return ts, fake
}

func translateAs(t *testing.T, ts *testSetup, s *models.Status, lang string) (int, map[string]any) {
	t.Helper()
	c, w := newGinContext(t, gin.Params{{Key: "id", Value: strconv.FormatUint(s.Id, 10)}, {Key: "lang", Value: lang}})
	setAccount(c, ts.acct)
	safeHandler(c, Translate)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

func TestTranslate_LLMPrivatePostsRestrictedByDefault(t *testing.T) {
	ts, fake := setupFakeLLM(t, map[string]any{translation.KeyAllowPrivate: false})
	require.True(t, translation.PublicOnly())
	for _, vis := range []uint8{models.StatusVisibilityPrivate, models.StatusVisibilityDirect} {
		s := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
			s.Language = "en"
			s.Visibility = vis
			s.Content = models.StatusContent{ContentFormat: "html", Content: "<p>Hello world</p>"}
		})
		code, body := translateAs(t, ts, s, "ja")
		require.Equal(t, 403, code, body)
		require.Equal(t, "translation_visibility_restricted", body["error_code"])
	}
	require.EqualValues(t, 0, fake.calls.Load())

	// Unlisted is allowed.
	s := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Language = "en"
		s.Visibility = models.StatusVisibilityUnlisted
		s.Content = models.StatusContent{ContentFormat: "html", Content: "<p>Hello world</p>"}
	})
	code, _ := translateAs(t, ts, s, "ja")
	require.Equal(t, 200, code)
}

func TestTranslate_LLMPrivatePostsAllowedWhenToggled(t *testing.T) {
	ts, _ := setupFakeLLM(t, map[string]any{translation.KeyAllowPrivate: true})
	require.False(t, translation.PublicOnly())
	s := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Language = "en"
		s.Visibility = models.StatusVisibilityPrivate
		s.Content = models.StatusContent{ContentFormat: "html", Content: "<p>Hello world</p>"}
	})
	code, body := translateAs(t, ts, s, "ja")
	require.Equal(t, 200, code, body)
}

func countCache(t *testing.T, ts *testSetup) int64 {
	t.Helper()
	var n int64
	require.NoError(t, ts.db.Model(&llmsvc.TranslationCache{}).Count(&n).Error)
	return n
}

func TestTranslate_CachePurgedOnDeleteAndEdit(t *testing.T) {
	ts, fake := setupFakeLLM(t, nil)
	mk := func(text string) *models.Status {
		return ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
			s.Language = "en"
			s.Content = models.StatusContent{ContentFormat: "html", Content: "<p>Hello world " + text + "</p>"}
		})
	}

	// Delete: both target languages of the status's content are purged.
	gone := mk("one")
	for _, lang := range []string{"ja", "fr"} {
		code, _ := translateAs(t, ts, gone, lang)
		require.Equal(t, 200, code)
	}
	kept := mk("two")
	code, _ := translateAs(t, ts, kept, "ja")
	require.Equal(t, 200, code)
	require.EqualValues(t, 3, countCache(t, ts))
	require.NoError(t, ts.db.Delete(gone).Error)
	require.EqualValues(t, 1, countCache(t, ts), "only the unrelated status's entry remains")

	// Edit: the old content's entries are purged and the new text re-translates.
	before := fake.calls.Load()
	kept.Content.Content = "<p>Hello world edited</p>"
	require.NoError(t, ts.db.Save(kept).Error)
	require.EqualValues(t, 0, countCache(t, ts))
	code, _ = translateAs(t, ts, kept, "ja")
	require.Equal(t, 200, code)
	require.Equal(t, before+1, fake.calls.Load())
}

func TestTranslate_PruneExpiredCache(t *testing.T) {
	ts, _ := setupFakeLLM(t, nil)
	old := llmsvc.TranslationCache{CacheKey: strings.Repeat("a", 64), ContentHash: "x", TargetLang: "ja", Result: "e30=", CreatedAt: time.Now().Add(-31 * 24 * time.Hour)}
	fresh := llmsvc.TranslationCache{CacheKey: strings.Repeat("b", 64), ContentHash: "y", TargetLang: "ja", Result: "e30=", CreatedAt: time.Now()}
	require.NoError(t, ts.db.Create(&old).Error)
	require.NoError(t, ts.db.Create(&fresh).Error)
	translation.PruneExpiredCache()
	require.EqualValues(t, 1, countCache(t, ts))
}
