package homeserver

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/llmsvc"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/kasuganosora/community/utils/translation"
	"github.com/stretchr/testify/require"
)

type groupFakeTrad struct{ calls atomic.Int32 }

func (f *groupFakeTrad) Translate(text, _, target string) (*translation.Result, error) {
	f.calls.Add(1)
	return &translation.Result{Text: "[" + target + "] " + text, DetectedSource: "en", Provider: "mt-gateway"}, nil
}
func (f *groupFakeTrad) DetectLanguage(string) (string, error) { return "en", nil }
func (f *groupFakeTrad) Name() string                          { return "mt-gateway" }

func groupTranslateSettings(t *testing.T, values map[string]any) {
	t.Helper()
	for k, v := range values {
		old := systemsettings.Get(k, nil)
		systemsettings.Set(k, v)
		t.Cleanup(func() { systemsettings.Set(k, old) })
	}
}

func groupTranslateFixture(t *testing.T) (*HomeServer, *gin.Engine, string, *groupFakeTrad) {
	t.Helper()
	hs, _, room := webMessageFixture(t)
	require.NoError(t, db.GetDB().AutoMigrate(llmsvc.Models()...))
	trad := &groupFakeTrad{}
	translation.SetTraditionalServiceForTest(func() *translation.Service { return translation.NewServiceWithProvider(trad) })
	translation.SetFallbackServiceForTest(func() (*translation.Service, bool) { return translation.NewServiceWithProvider(trad), true })
	t.Cleanup(func() {
		translation.SetTraditionalServiceForTest(nil)
		translation.SetFallbackServiceForTest(nil)
	})
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("web_group_identity", true)
		c.Set("user_id", c.GetHeader("X-Test-User"))
		c.Set("web_group_account", &models.Account{Id: 7, Username: "alice"})
	})
	registerWebGroupMessageRoutes(r.Group("/api/v1/chat"))
	return hs, r, room, trad
}

func TestWebGroupTranslatePolicyCacheAndRedaction(t *testing.T) {
	hs, r, room, trad := groupTranslateFixture(t)
	groupTranslateSettings(t, map[string]any{systemsettings.KeyIsEnableTranslation: true, translation.KeyMode: translation.ModeTraditional, translation.KeyGroupMessages: nil, translation.KeyFallback: nil})
	msg := webMessageSend(t, r, room, "t1", "Hello world, this is the godot group")
	require.Equal(t, "en", msg.Language)
	call := func(user string) (int, map[string]any) {
		w := webMessageRequest(t, r, "POST", room, "/messages/"+msg.ID+"/translate", user, map[string]string{"lang": "ja"})
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body
	}
	// Default ON: traditional primary at an explicit (self-hosted) URL.
	code, body := call("@alice:test.local")
	require.Equal(t, 200, code, body)
	require.Equal(t, "traditional", body["engine"])
	require.Contains(t, body["content"], "[ja] Hello world")
	code, _ = call("@alice:test.local")
	require.Equal(t, 200, code)
	require.EqualValues(t, 1, trad.calls.Load(), "second call served from cache")
	var refs int64
	db.GetDB().Model(&llmsvc.GroupTranslationCacheRef{}).Where("event_id = ?", msg.ID).Count(&refs)
	require.EqualValues(t, 1, refs)

	// Non-members cannot translate.
	code, _ = call("@mallory:test.local")
	require.Equal(t, 403, code)

	// Admin switch off.
	groupTranslateSettings(t, map[string]any{translation.KeyGroupMessages: false})
	code, body = call("@alice:test.local")
	require.Equal(t, 403, code)
	require.Equal(t, "translation_disabled", body["error_code"])
	groupTranslateSettings(t, map[string]any{translation.KeyGroupMessages: true})

	// LLM primary, private room, llm_allow_private off, no fallback: restricted.
	groupTranslateSettings(t, map[string]any{translation.KeyMode: translation.ModeLLM, translation.KeyFallback: false, translation.KeyAllowPrivate: false})
	if _, ok := llmsvc.ServiceProvider(llmsvc.ServiceTranslation); ok {
		code, body = call("@alice:test.local")
		require.Equal(t, 403, code)
		require.Equal(t, "translation_visibility_restricted", body["error_code"])
	}
	groupTranslateSettings(t, map[string]any{translation.KeyMode: translation.ModeTraditional})

	// Redaction purges cached translations.
	var caches int64
	db.GetDB().Model(&llmsvc.TranslationCache{}).Count(&caches)
	require.EqualValues(t, 1, caches)
	w := webMessageRequest(t, r, "DELETE", room, "/messages/"+msg.ID, "@alice:test.local", nil)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Eventually(t, func() bool {
		db.GetDB().Model(&llmsvc.TranslationCache{}).Count(&caches)
		return caches == 0
	}, 5*time.Second, 20*time.Millisecond)
	code, body = call("@alice:test.local")
	require.Equal(t, 422, code, body)
	_ = hs
}

func TestWebGroupTranslateEditDropsOldVersion(t *testing.T) {
	_, r, room, trad := groupTranslateFixture(t)
	groupTranslateSettings(t, map[string]any{systemsettings.KeyIsEnableTranslation: true, translation.KeyMode: translation.ModeTraditional, translation.KeyGroupMessages: true})
	msg := webMessageSend(t, r, room, "e1", "Hello world first version")
	w := webMessageRequest(t, r, "POST", room, "/messages/"+msg.ID+"/translate", "@alice:test.local", map[string]string{"lang": "fr"})
	require.Equal(t, 200, w.Code, w.Body.String())
	// A Matrix client edit (m.replace) changes the content hash.
	edit := MatrixEvent{RoomID: room, Sender: "@alice:test.local", EventType: EventTypeRoomMessage, OriginServerTS: time.Now().Add(time.Minute).UnixMilli(), Content: map[string]any{"msgtype": "m.text", "body": "* Hello world second version", "m.new_content": map[string]any{"msgtype": "m.text", "body": "Hello world second version"}, "m.relates_to": map[string]any{"rel_type": "m.replace", "event_id": msg.ID}}}
	require.NoError(t, db.GetDB().Create(&edit).Error)
	w = webMessageRequest(t, r, "POST", room, "/messages/"+msg.ID+"/translate", "@alice:test.local", map[string]string{"lang": "fr"})
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), "second version")
	require.EqualValues(t, 2, trad.calls.Load())
	var refs int64
	db.GetDB().Model(&llmsvc.GroupTranslationCacheRef{}).Where("event_id = ?", msg.ID).Count(&refs)
	require.EqualValues(t, 1, refs, "old version's translation purged")
}

func TestWebGroupStickerDetection(t *testing.T) {
	small := map[string]any{"mimetype": "video/webm", "size": float64(40000), "w": float64(512), "h": float64(512), "fi.mau.loop": true}
	cases := []struct {
		name    string
		typ     EventType
		content map[string]any
		want    bool
	}{
		{"native", "m.sticker", map[string]any{"url": "mxc://x/a", "body": "(weary face)"}, true},
		{"telegram meta", EventTypeRoomMessage, map[string]any{"url": "mxc://x/a", "msgtype": "m.video", "info": map[string]any{"mimetype": "video/webm", "fi.mau.telegram.animated_sticker": true}}, true},
		{"tgs", EventTypeRoomMessage, map[string]any{"url": "mxc://x/a", "msgtype": "m.file", "info": map[string]any{"mimetype": "application/x-tgsticker"}}, true},
		{"small loop webm", EventTypeRoomMessage, map[string]any{"url": "mxc://x/a", "msgtype": "m.video", "info": small}, true},
		{"plain video", EventTypeRoomMessage, map[string]any{"url": "mxc://x/a", "msgtype": "m.video", "info": map[string]any{"mimetype": "video/webm", "size": float64(9 << 20), "w": float64(1280), "h": float64(720)}}, false},
		{"text", EventTypeRoomMessage, map[string]any{"msgtype": "m.text", "body": "sticker"}, false},
	}
	for _, c := range cases {
		require.Equal(t, c.want, webGroupIsSticker(c.typ, c.content), c.name)
	}
}
