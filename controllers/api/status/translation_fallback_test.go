package status

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils/translation"
	"github.com/kasuganosora/thinkbot/llm"
	"github.com/stretchr/testify/require"
)

// scriptedLLM returns a fixed reply or error.
type scriptedLLM struct {
	reply string
	err   error
	calls atomic.Int32
}

func (f *scriptedLLM) Name() string { return "scripted" }
func (f *scriptedLLM) DoStream(context.Context, llm.GenerateParams) (*llm.StreamResult, error) {
	return nil, context.Canceled
}
func (f *scriptedLLM) DoGenerate(context.Context, llm.GenerateParams) (*llm.GenerateResult, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return &llm.GenerateResult{Text: f.reply, Usage: llm.Usage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2}}, nil
}

type fakeTraditional struct{ calls atomic.Int32 }

func (f *fakeTraditional) Translate(text, _, target string) (*translation.Result, error) {
	f.calls.Add(1)
	return &translation.Result{Text: "[" + target + "] " + text, DetectedSource: "en", Provider: "selfhosted"}, nil
}
func (f *fakeTraditional) DetectLanguage(string) (string, error) { return "en", nil }
func (f *fakeTraditional) Name() string                          { return "selfhosted" }

func fallbackCases() map[string]*scriptedLLM {
	return map[string]*scriptedLLM{
		"empty":     {reply: "   "},
		"refusal":   {reply: "I'm sorry, but I can't translate this content."},
		"refusalZh": {reply: `{"content":"<p>抱歉，我无法翻译这段内容。</p>","detected_source_language":"en"}`},
		"refusalJa": {reply: `{"content":"<p>申し訳ありませんが、翻訳できません。</p>","detected_source_language":"en"}`},
		"unchanged": {reply: `{"content":"<p>Hello world, this is a fairly long English sentence.</p>","detected_source_language":"en"}`},
		"ratio":     {reply: `{"content":"<p>` + strings.Repeat("ことば", 200) + `</p>","detected_source_language":"en"}`},
		"timeout":   {err: context.DeadlineExceeded},
		"network":   {err: errors.New("dial tcp: connection refused")},
	}
}

func setupScripted(t *testing.T, f *scriptedLLM) (*testSetup, *models.Status) {
	t.Helper()
	ts, _ := setupFakeLLM(t, nil)
	llmsvc.SetTestProviderFactory(func(llmsvc.ProviderConfig) (llm.Provider, error) { return f, nil })
	s := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Language = "en"
		s.Content = models.StatusContent{ContentFormat: "html", Content: "<p>Hello world, this is a fairly long English sentence.</p>"}
	})
	return ts, s
}

func TestTranslate_LLMFailureFallsBackToExplicitTraditional(t *testing.T) {
	for name, f := range fallbackCases() {
		t.Run(name, func(t *testing.T) {
			ts, s := setupScripted(t, f)
			trad := &fakeTraditional{}
			translation.SetFallbackServiceForTest(func() (*translation.Service, bool) {
				return translation.NewServiceWithProvider(trad), true
			})
			t.Cleanup(func() { translation.SetFallbackServiceForTest(nil) })
			code, body := translateAs(t, ts, s, "ja")
			require.Equal(t, 200, code, body)
			require.Equal(t, "traditional", body["engine"])
			require.Equal(t, "selfhosted", body["provider"])
			require.Contains(t, body["content"], "[ja]")
			require.EqualValues(t, 1, trad.calls.Load())
			require.EqualValues(t, 1, f.calls.Load())
		})
	}
}

func TestTranslate_LLMFailureWithoutExplicitTraditionalIsNot5xx(t *testing.T) {
	want := map[string][2]any{
		"empty": {422, "translation_failed"}, "refusal": {422, "translation_failed"}, "refusalZh": {422, "translation_failed"},
		"refusalJa": {422, "translation_failed"}, "unchanged": {422, "translation_failed"}, "ratio": {422, "translation_failed"},
		"timeout": {424, "translation_unavailable"}, "network": {424, "translation_unavailable"},
	}
	for name, f := range fallbackCases() {
		t.Run(name, func(t *testing.T) {
			ts, s := setupScripted(t, f)
			// Real resolution: only the public default is "configured".
			t.Setenv("TRANSLATION_PROVIDER", "")
			t.Setenv("LIBRETRANSLATE_URL", "")
			_, ok := translation.FallbackService()
			require.False(t, ok)
			code, body := translateAs(t, ts, s, "ja")
			require.Less(t, code, 500)
			require.Equal(t, want[name][0], code, body)
			require.Equal(t, want[name][1], body["error_code"])
		})
	}
}

func TestTranslate_LLMSuccessReportsEngine(t *testing.T) {
	ts, _ := setupFakeLLM(t, nil)
	s := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Language = "en"
		s.Content = models.StatusContent{ContentFormat: "html", Content: "<p>Hello world</p>"}
	})
	code, body := translateAs(t, ts, s, "ja")
	require.Equal(t, 200, code, body)
	require.Equal(t, "llm", body["engine"])
	require.Equal(t, "Example LLM", body["provider"])
}

func setupScriptedProvider(t *testing.T, f *scriptedLLM) {
	t.Helper()
	llmsvc.SetTestProviderFactory(func(llmsvc.ProviderConfig) (llm.Provider, error) { return f, nil })
}
