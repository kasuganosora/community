package status

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils/translation"
	"github.com/stretchr/testify/require"
)

// gatewayTraditional mimics mt-gateway: it reports the engine and detected
// language, and records the source and hint it was sent.
type gatewayTraditional struct {
	mu      sync.Mutex
	reply   func(text string) string
	engine  string
	sources []string
	hints   []string
}

func (g *gatewayTraditional) Translate(text, source, target string) (*translation.Result, error) {
	return g.TranslateContext(context.Background(), text, source, target)
}

func (g *gatewayTraditional) TranslateContext(ctx context.Context, text, source, _ string) (*translation.Result, error) {
	g.mu.Lock()
	g.sources = append(g.sources, source)
	g.hints = append(g.hints, translation.SourceHintForTest(ctx))
	g.mu.Unlock()
	return &translation.Result{Text: g.reply(text), DetectedSource: "en", Provider: "libretranslate", Engine: g.engine}, nil
}
func (g *gatewayTraditional) DetectLanguage(string) (string, error) { return "en", nil }
func (g *gatewayTraditional) Name() string                          { return "libretranslate" }

const fictionalEnglish = "<p>@robin, specifically about the noise from the garden pump hahaha</p>"

func remoteStatus(ts *testSetup, lang, content string) *models.Status {
	return ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		uri := "https://remote.example/statuses/1"
		s.OriginUri = &uri
		s.Language = lang
		s.Visibility = models.StatusVisibilityPublic
		s.Content = models.StatusContent{ContentFormat: "html", Content: content}
	})
}

func cacheRows(t *testing.T) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.GetDB().Model(&llmsvc.TranslationCache{}).Count(&n).Error)
	return n
}

func useGateway(t *testing.T, g *gatewayTraditional) {
	translation.SetTraditionalServiceForTest(func() *translation.Service { return translation.NewServiceWithProvider(g) })
	t.Cleanup(func() { translation.SetTraditionalServiceForTest(nil) })
}

// An echo with only the comma turned fullwidth is not a translation: without
// a fallback it is a 422 and nothing is cached.
func TestTranslateTraditional_NearEchoIsFailureAndNotCached(t *testing.T) {
	ts, fake := setupFakeLLM(t, map[string]any{translation.KeyMode: translation.ModeTraditional, translation.KeyFallback: nil})
	g := &gatewayTraditional{engine: "hy-mt", reply: func(s string) string { return strings.Replace(s, ",", "，", 1) }}
	useGateway(t, g)
	before := cacheRows(t)
	code, body := translateAs(t, ts, remoteStatus(ts, "pt", fictionalEnglish), "zh-CN")
	require.Equal(t, 422, code, body)
	require.Equal(t, "translation_failed", body["error_code"])
	require.Equal(t, before, cacheRows(t))
	require.EqualValues(t, 0, fake.calls.Load())
}

func TestTranslateTraditional_WrongScriptFallsBackToLLM(t *testing.T) {
	ts, fake := setupFakeLLM(t, map[string]any{translation.KeyMode: translation.ModeTraditional, translation.KeyFallback: true})
	g := &gatewayTraditional{engine: "hy-mt", reply: func(string) string { return "<p>Specifically regarding the pump noise, haha</p>" }}
	useGateway(t, g)
	code, body := translateAs(t, ts, remoteStatus(ts, "pt", fictionalEnglish), "ja")
	require.Equal(t, 200, code, body)
	require.Equal(t, "llm", body["engine"])
	require.EqualValues(t, 1, fake.calls.Load())
}

// Remote language tags are untrusted: send source=auto with the tag as a
// hint, and label with the engine and language the gateway reports.
func TestTranslateTraditional_RemoteSendsAutoAndShowsGatewayEngine(t *testing.T) {
	ts, _ := setupFakeLLM(t, map[string]any{translation.KeyMode: translation.ModeTraditional, translation.KeyFallback: nil})
	g := &gatewayTraditional{engine: "hy-mt", reply: func(string) string { return "<p>@robin，特别是关于花园水泵的噪音，哈哈哈哈</p>" }}
	useGateway(t, g)
	code, body := translateAs(t, ts, remoteStatus(ts, "pt", fictionalEnglish), "zh-CN")
	require.Equal(t, 200, code, body)
	require.Equal(t, "hy-mt", body["provider"])
	require.Equal(t, "en", body["detected_source_language"])
	require.Equal(t, []string{"auto"}, g.sources)
	require.Equal(t, []string{"pt"}, g.hints)
}

func TestTranslateTraditional_LocalKeepsDeclaredSource(t *testing.T) {
	ts, _ := setupFakeLLM(t, map[string]any{translation.KeyMode: translation.ModeTraditional, translation.KeyFallback: nil})
	g := &gatewayTraditional{reply: func(string) string { return "<p>花园水泵的噪音特别大，哈哈哈哈哈</p>" }}
	useGateway(t, g)
	s := ts.fix.CreateStatus(ts.acct, func(s *models.Status) {
		s.Language = "en"
		s.Content = models.StatusContent{ContentFormat: "html", Content: fictionalEnglish}
	})
	code, body := translateAs(t, ts, s, "zh-CN")
	require.Equal(t, 200, code, body)
	require.Equal(t, "libretranslate", body["provider"], "no engine reported: provider name")
	require.Equal(t, []string{"en"}, g.sources)
	require.Equal(t, []string{""}, g.hints)
}

func TestFindUnusableCache_PurgesEchoesOnly(t *testing.T) {
	ts, _ := setupFakeLLM(t, map[string]any{translation.KeyMode: translation.ModeTraditional})
	s := remoteStatus(ts, "pt", fictionalEnglish)
	conn := db.GetDB()
	add := func(key, hash, target, content string) uint64 {
		raw := `{"content":` + strconvQuote(content) + `,"detected_source_language":"","provider":"libretranslate"}`
		row := llmsvc.TranslationCache{CacheKey: key, ContentHash: hash, TargetLang: target, Provider: "libretranslate", Result: base64Std(raw)}
		require.NoError(t, conn.Create(&row).Error)
		return row.ID
	}
	require.NoError(t, conn.Create(&llmsvc.TranslationCacheRef{StatusID: s.Id, ContentHash: "h-status"}).Error)
	echo := add("k-echo", "h-status", "zh-CN", strings.Replace(fictionalEnglish, ",", "，", 1))
	good := add("k-good", "h-status", "ja", "<p>@robin、特に庭のポンプの音についてです笑</p>")
	group := add("k-group", "h-group", "zh-CN", "<p>Group message that was never translated at all</p>")
	groupOK := add("k-group-ok", "h-group2", "fr", "<p>Message de groupe</p>")

	_, found, err := translation.FindUnusableCache(false)
	require.NoError(t, err)
	ids := map[uint64]string{}
	for _, e := range found {
		ids[e.ID] = e.Reason
	}
	require.Equal(t, map[uint64]string{echo: "unchanged_output", group: "wrong_script"}, ids)

	_, _, err = translation.FindUnusableCache(true)
	require.NoError(t, err)
	var left []uint64
	require.NoError(t, conn.Model(&llmsvc.TranslationCache{}).Where("id IN ?", []uint64{echo, good, group, groupOK}).Order("id").Pluck("id", &left).Error)
	require.Equal(t, []uint64{good, groupOK}, left)
}

func strconvQuote(s string) string { return strconv.Quote(s) }
func base64Std(s string) string    { return base64.StdEncoding.EncodeToString([]byte(s)) }

// A LibreTranslate-compatible backend answering with a fixed status/body.
func useHTTPGateway(t *testing.T, status int, body string) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LIBRETRANSLATE_URL", srv.URL)
	p := translation.NewLibreTranslateProvider()
	translation.SetTraditionalServiceForTest(func() *translation.Service { return translation.NewServiceWithProvider(p) })
	t.Cleanup(func() { translation.SetTraditionalServiceForTest(nil) })
	return &calls
}

func TestTraditionalBackendErrorsMapToStableCodes(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		wantCode   string
	}{
		{"unsupported pair (gateway)", 422, `{"error":"translation failed on all engines","code":"translation_language_unsupported"}`, 422, "translation_language_unsupported"},
		{"unsupported pair (plain LibreTranslate)", 400, `{"error":"nb is not supported"}`, 422, "translation_language_unsupported"},
		{"echo refused by gateway", 422, `{"error":"translation failed on all engines","code":"translation_unchanged"}`, 422, "translation_failed"},
		{"outage", 502, `{"error":"translation failed on all engines","code":"libretranslate_unavailable"}`, 503, "translation_unavailable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts, fake := setupFakeLLM(t, map[string]any{translation.KeyMode: translation.ModeTraditional, translation.KeyFallback: nil})
			useHTTPGateway(t, c.status, c.body)
			before := cacheRows(t)
			code, body := translateAs(t, ts, remoteStatus(ts, "nb", "<p>Et fiktivt notat om hagepumpen som bråker</p>"), "ko")
			require.Equal(t, c.wantStatus, code, body)
			require.Equal(t, c.wantCode, body["error_code"])
			require.Equal(t, before, cacheRows(t))
			require.EqualValues(t, 0, fake.calls.Load())
		})
	}
}

func TestTraditionalGatewayFieldsUsed(t *testing.T) {
	ts, _ := setupFakeLLM(t, map[string]any{translation.KeyMode: translation.ModeTraditional, translation.KeyFallback: nil})
	useHTTPGateway(t, 200, `{"translatedText":"<p>关于花园水泵噪音的虚构说明</p>","engine":"hy-mt","detectedLanguage":{"language":"en","confidence":92}}`)
	code, body := translateAs(t, ts, remoteStatus(ts, "pt", "<p>A fictional note about the garden pump noise</p>"), "zh-CN")
	require.Equal(t, 200, code, body)
	require.Equal(t, "hy-mt", body["provider"])
	require.Equal(t, "en", body["detected_source_language"])
}
