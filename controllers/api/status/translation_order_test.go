package status

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils/translation"
	"github.com/stretchr/testify/require"
)

// scriptedTraditional returns a fixed result or error.
type scriptedTraditional struct {
	text  string
	err   error
	calls atomic.Int32
}

func (f *scriptedTraditional) Translate(text, _, target string) (*translation.Result, error) {
	f.calls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	out := f.text
	if out == "=" {
		out = text
	} else if out == "" {
		out = "[" + target + "] " + text
	}
	return &translation.Result{Text: out, DetectedSource: "en", Provider: "mt-gateway"}, nil
}
func (f *scriptedTraditional) DetectLanguage(string) (string, error) { return "en", nil }
func (f *scriptedTraditional) Name() string                          { return "mt-gateway" }

func setupTraditionalPrimary(t *testing.T, trad *scriptedTraditional, extra map[string]any) (*testSetup, *fakeLLM) {
	t.Helper()
	values := map[string]any{translation.KeyMode: translation.ModeTraditional}
	for k, v := range extra {
		values[k] = v
	}
	ts, fake := setupFakeLLM(t, values)
	translation.SetTraditionalServiceForTest(func() *translation.Service { return translation.NewServiceWithProvider(trad) })
	t.Cleanup(func() { translation.SetTraditionalServiceForTest(nil) })
	return ts, fake
}

func orderStatus(ts *testSetup, visibility uint8) *models.Status {
	return ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Language = "en"
		s.Visibility = visibility
		s.Content = models.StatusContent{ContentFormat: "html", Content: "<p>Hello world, a longer English sentence.</p>"}
	})
}

func TestTranslateOrder_TraditionalPrimarySucceeds(t *testing.T) {
	trad := &scriptedTraditional{}
	ts, fake := setupTraditionalPrimary(t, trad, map[string]any{translation.KeyFallback: true})
	code, body := translateAs(t, ts, orderStatus(ts, models.StatusVisibilityPublic), "ja")
	require.Equal(t, 200, code, body)
	require.Equal(t, "traditional", body["engine"])
	require.EqualValues(t, 0, fake.calls.Load())
}

func TestTranslateOrder_TraditionalPrimaryFallsBackToLLM(t *testing.T) {
	for name, trad := range map[string]*scriptedTraditional{
		"error":     {err: errors.New("timeout")},
		"unchanged": {text: "="},
		"empty":     {text: " "},
	} {
		t.Run(name, func(t *testing.T) {
			ts, fake := setupTraditionalPrimary(t, trad, map[string]any{translation.KeyFallback: true})
			code, body := translateAs(t, ts, orderStatus(ts, models.StatusVisibilityPublic), "ja")
			require.Equal(t, 200, code, body)
			require.Equal(t, "llm", body["engine"])
			require.Contains(t, body["content"], "こんにちは世界")
			require.EqualValues(t, 1, fake.calls.Load())
			require.EqualValues(t, 1, trad.calls.Load())
		})
	}
}

func TestTranslateOrder_TraditionalPrimaryWithoutFallback(t *testing.T) {
	trad := &scriptedTraditional{err: errors.New("down")}
	// Unset fallback defaults to off for a traditional primary.
	ts, fake := setupTraditionalPrimary(t, trad, map[string]any{translation.KeyFallback: nil})
	code, body := translateAs(t, ts, orderStatus(ts, models.StatusVisibilityPublic), "ja")
	require.Equal(t, 503, code, body)
	require.Equal(t, "translation_unavailable", body["error_code"])
	require.EqualValues(t, 0, fake.calls.Load())
}

func TestTranslateOrder_LLMFallbackRespectsPrivacy(t *testing.T) {
	trad := &scriptedTraditional{err: errors.New("down")}
	ts, fake := setupTraditionalPrimary(t, trad, map[string]any{translation.KeyFallback: true, translation.KeyAllowPrivate: false})
	code, body := translateAs(t, ts, orderStatus(ts, models.StatusVisibilityPrivate), "ja")
	require.Equal(t, 503, code, body)
	require.EqualValues(t, 0, fake.calls.Load(), "private post must not reach the LLM")
}

func TestTranslateOrder_LLMPrimaryFallbackDisabled(t *testing.T) {
	ts, _ := setupFakeLLM(t, map[string]any{translation.KeyFallback: false})
	s := orderStatus(ts, models.StatusVisibilityPublic)
	setupScriptedProvider(t, fallbackCases()["network"])
	trad := &fakeTraditional{}
	translation.SetFallbackServiceForTest(func() (*translation.Service, bool) { return translation.NewServiceWithProvider(trad), true })
	t.Cleanup(func() { translation.SetFallbackServiceForTest(nil) })
	code, body := translateAs(t, ts, s, "ja")
	require.Equal(t, 424, code, body)
	require.EqualValues(t, 0, trad.calls.Load())
}
