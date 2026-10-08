package streaming

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/controllers"
	"github.com/kasuganosora/community/db/database"
	cRedis "github.com/kasuganosora/community/db/redis"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/serializer"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// ---------------------------------------------------------------------------
// parseAllowedOrigins
// ---------------------------------------------------------------------------

func TestParseAllowedOrigins(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"empty", "", nil},
		{"single", "https://example.com", []string{"https://example.com"}},
		{"multiple", "https://a.com, https://b.com", []string{"https://a.com", "https://b.com"}},
		{"with spaces", "  https://a.com  ,  http://b.com  ", []string{"https://a.com", "http://b.com"}},
		{"trailing comma", "https://a.com,", []string{"https://a.com"}},
		{"only commas", ",,,", []string{}},
		{"mixed empty and valid", ",https://valid.com,,", []string{"https://valid.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAllowedOrigins(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseAllowedOrigins_Capacity(t *testing.T) {
	result := parseAllowedOrigins("https://a.com,https://b.com,https://c.com")
	require.Len(t, result, 3)
	assert.Contains(t, result, "https://a.com")
	assert.Contains(t, result, "https://b.com")
	assert.Contains(t, result, "https://c.com")
}

// ---------------------------------------------------------------------------
// checkOrigin
// ---------------------------------------------------------------------------

func TestCheckOrigin_EmptyOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Del("Origin")
	assert.True(t, checkOrigin(req))
}

func TestCheckOrigin_NoAllowedOriginsConfigured(t *testing.T) {
	origVal := config.C.WebSocketAllowedOrigins
	config.C.WebSocketAllowedOrigins = ""
	defer func() { config.C.WebSocketAllowedOrigins = origVal }()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	req.Host = "localhost:8080"
	// No allowed origins configured -> falls back to r.Host != ""
	assert.True(t, checkOrigin(req))

	req.Host = ""
	assert.False(t, checkOrigin(req))
}

func TestCheckOrigin_AllowedOrigin(t *testing.T) {
	origVal := config.C.WebSocketAllowedOrigins
	config.C.WebSocketAllowedOrigins = "example.com,foo.bar.com"
	defer func() { config.C.WebSocketAllowedOrigins = origVal }()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://example.com")
	assert.True(t, checkOrigin(req))
}

func TestCheckOrigin_DisallowedOrigin(t *testing.T) {
	origVal := config.C.WebSocketAllowedOrigins
	config.C.WebSocketAllowedOrigins = "example.com"
	defer func() { config.C.WebSocketAllowedOrigins = origVal }()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Origin", "https://evil.com")
	assert.False(t, checkOrigin(req))
}

func TestCheckOrigin_InvalidOriginURL(t *testing.T) {
	origVal := config.C.WebSocketAllowedOrigins
	config.C.WebSocketAllowedOrigins = "example.com"
	defer func() { config.C.WebSocketAllowedOrigins = origVal }()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// Set an invalid Origin that url.Parse will reject
	req.Header.Set("Origin", "://bad-url")
	assert.False(t, checkOrigin(req))
}

// ---------------------------------------------------------------------------
// serializerStatusEvent
// ---------------------------------------------------------------------------

// setupTestDB injects an in-memory SQLite DB into database.DB so that
// db.GetDB() returns a usable handle for functions that need it
// (e.g. Status.CheckAndLoadAllExtendFields). It also auto-migrates the
// models that CheckAndLoadAllExtendFields touches.
func setupTestDB(t *testing.T) {
	t.Helper()

	gdb, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Auto-migrate models used by CheckAndLoadAllExtendFields
	require.NoError(t, gdb.AutoMigrate(
		&models.Status{},
		&models.StatusContent{},
		&models.Account{},
		&models.Application{},
		&models.Attachment{},
		&models.Emoji{},
		&models.Tag{},
		&models.Poll{},
		&models.PollOption{},
	))

	database.SetTestDB(gdb)
	t.Cleanup(func() {
		if sqlDB, err := gdb.DB(); err == nil {
			_ = sqlDB.Close()
		}

		database.ResetTestDB()
	})
}

func TestSerializerStatusEvent_DeleteEvent(t *testing.T) {
	setupTestDB(t)

	// delete event only needs the status id; payload check is minimal
	payload := `{"type":"delete","payload":{"id":12345}}`

	eventType, payloadStr, err := serializerStatusEvent(nil, payload)
	// With migrated DB schema, CheckAndLoadAllExtendFields should succeed
	// (poll query returns ErrRecordNotFound which is handled).
	require.NoError(t, err)
	assert.Equal(t, "delete", eventType)
	assert.Equal(t, "12345", payloadStr)
}

func TestSerializerStatusEvent_InvalidJSON(t *testing.T) {
	setupTestDB(t)

	_, _, err := serializerStatusEvent(nil, "not json")
	assert.Error(t, err)
}

func TestSerializerStatusEvent_LoadedStatus(t *testing.T) {
	setupTestDB(t)

	// A status with SetAllExtendFieldsLoaded set to true will skip the
	// CheckAndLoadAllExtendFields DB queries entirely, allowing the
	// serialization path to run without needing a full DB schema.
	status := &models.Status{
		Id:         100,
		AccountId:  1,
		Visibility: models.StatusVisibilityPublic,
	}
	status.SetAllExtendFieldsLoaded()
	status.Account = &models.Account{Id: 1, Username: "tester"}

	payloadBytes, _ := json.Marshal(map[string]any{
		"type":    "update",
		"payload": status,
	})

	// ToStatus may still access DB for filters, but account is nil so it
	// should return early. We just verify no panic.
	assert.NotPanics(t, func() {
		_, _, _ = serializerStatusEvent(nil, string(payloadBytes))
	})
}

// ---------------------------------------------------------------------------
// serializerNotificationEvent
// ---------------------------------------------------------------------------

func TestSerializerNotificationEvent_Valid(t *testing.T) {
	now := time.Now()
	notif := &serializer.Notification{
		Id:        "42",
		Type:      "mention",
		CreatedAt: &now,
	}

	payloadBytes, err := json.Marshal(map[string]any{"payload": notif})
	require.NoError(t, err)

	eventType, payloadStr, err := serializerNotificationEvent(nil, string(payloadBytes))
	require.NoError(t, err)
	assert.Equal(t, "notification", eventType)
	assert.Contains(t, payloadStr, "42")
	assert.Contains(t, payloadStr, "mention")
}

func TestSerializerNotificationEvent_InvalidJSON(t *testing.T) {
	_, _, err := serializerNotificationEvent(nil, "invalid json")
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Announcement event serializers
// ---------------------------------------------------------------------------

func TestSerializerAnnouncementEvent(t *testing.T) {
	payload := json.RawMessage(`{"id":"1","content":"hello"}`)
	eventType, payloadStr, err := serializerAnnouncementEvent(payload)
	require.NoError(t, err)
	assert.Equal(t, "announcement", eventType)
	assert.Equal(t, `{"id":"1","content":"hello"}`, payloadStr)
}

func TestSerializerAnnouncementDeleteEvent(t *testing.T) {
	payload := json.RawMessage(`"1"`)
	eventType, payloadStr, err := serializerAnnouncementDeleteEvent(payload)
	require.NoError(t, err)
	assert.Equal(t, "announcement.delete", eventType)
	assert.Equal(t, `"1"`, payloadStr)
}

func TestSerializerAnnouncementReactionEvent(t *testing.T) {
	payload := json.RawMessage(`{"name":"👍","count":1}`)
	eventType, payloadStr, err := serializerAnnouncementReactionEvent(payload)
	require.NoError(t, err)
	assert.Equal(t, "announcement.reaction", eventType)
	assert.Contains(t, payloadStr, "👍")
}

// ---------------------------------------------------------------------------
// RedisChannelEvent struct
// ---------------------------------------------------------------------------

func TestRedisChannelEvent_JSON(t *testing.T) {
	ev := &RedisChannelEvent{
		Type:    "update",
		Payload: map[string]any{"id": 1},
	}

	data, err := json.Marshal(ev)
	require.NoError(t, err)
	assert.Contains(t, string(data), "update")

	var decoded RedisChannelEvent
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, "update", decoded.Type)
}

// ---------------------------------------------------------------------------
// Health handler
// ---------------------------------------------------------------------------

func setupRedisForTest(t *testing.T) *redis.Client {
	t.Helper()

	mr, err := miniredis.Run()
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
		mr.Close()
	})

	// Inject into the global redis pool so db.GetRedis() returns our client
	prev := cRedis.GetRedisesForTest()
	cRedis.SetRedisesForTest([]*redis.Client{client})
	t.Cleanup(func() {
		cRedis.SetRedisesForTest(prev)
	})

	// Reset feed subscription cache and service so they use our client
	feed.SetDefaultService(feed.NewFeedService(feed.NewRedisClient(client)))
	feed.ResetSubscriptionCache()

	return client
}

func TestHealth_OK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupRedisForTest(t)

	r := gin.New()
	r.GET("/health", Health)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "OK", w.Body.String())
}

// ---------------------------------------------------------------------------
// SSE streaming handlers (public, hashtag, list)
// ---------------------------------------------------------------------------

// startSseService subscribes to Redis and blocks on the context. To test it
// we publish a message to the channel and then cancel the request context to
// terminate the loop cleanly.

func TestStartSseService_PublicStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := setupRedisForTest(t)
	setupTestDB(t)
	ctx := t.Context()

	r := gin.New()
	r.GET("/api/v1/streaming/public", func(c *gin.Context) {
		account := &models.Account{Id: 1}
		timeline := &public.PublicTimeline{}
		procHandle := func(data any) (event, payload string, err error) {
			return serializerStatusEvent(account, data.(string)) //nolint:forcetypeassert
		}
		startSseService(c, procHandle, timeline)
	})

	// Publish a delete event to the public channel in a goroutine.
	// The delete path is simpler (doesn't need full serialization) so it
	// works with the minimal in-memory DB.
	go func() {
		time.Sleep(50 * time.Millisecond)
		channelName := feed.GetRedisChannelName(&public.PublicTimeline{})
		_ = client.Publish(ctx, channelName, `{"type":"delete","payload":{"id":999}}`).Err()
	}()

	// Cancel context to stop the SSE loop after reading
	ctx2, cancel := context.WithCancel(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/streaming/public", nil)
	req = req.WithContext(ctx2)

	w := httptest.NewRecorder()

	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
	assert.Contains(t, w.Header().Get("Cache-Control"), "no-cache")
	body := w.Body.String()
	// The delete event should have been processed and written to the SSE body
	assert.Contains(t, body, "event: delete")
	assert.Contains(t, body, "999")
}

func TestStartSseService_NoTimelinesPanics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	assert.Panics(t, func() {
		startSseService(c, func(data any) (event, payload string, err error) {
			return "e", "p", nil
		})
	})
}

// ---------------------------------------------------------------------------
// PublicStreamingBase handlers (error paths via httptest)
// ---------------------------------------------------------------------------

func TestPublicStreamingBase_BindError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(controllers.AbortRecoveryMiddleware())
	r.GET("/public", PublicStreamingAll)

	// malformed only_media value triggers binding error
	req := httptest.NewRequest(http.MethodGet, "/public?only_media=notabool", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

func TestHashtagStreamingBase_MissingTag(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(controllers.AbortRecoveryMiddleware())
	r.GET("/hashtag", HashtagStreamingAll)

	req := httptest.NewRequest(http.MethodGet, "/hashtag", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListStreaming_InvalidListId(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(controllers.AbortRecoveryMiddleware())
	r.GET("/list", ListStreaming)

	req := httptest.NewRequest(http.MethodGet, "/list?list=abc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}

// ---------------------------------------------------------------------------
// DirectStreaming / NotificationStreaming / UserStreaming handlers
// These call MustGetAccount which panics if no account in context.
// We test that the panic/abort happens when account is missing.
// ---------------------------------------------------------------------------

func TestUserStreaming_NoAccountPanics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(controllers.AbortRecoveryMiddleware())
	r.GET("/user", UserStreaming)

	req := httptest.NewRequest(http.MethodGet, "/user", nil)
	w := httptest.NewRecorder()

	// MustGetAccount will abort with 500 and panic the sentinel.
	// AbortRecoveryMiddleware swallows the sentinel panic.
	assert.NotPanics(t, func() {
		r.ServeHTTP(w, req)
	})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestNotificationStreaming_NoAccountPanics(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(controllers.AbortRecoveryMiddleware())
	r.GET("/notif", NotificationStreaming)

	req := httptest.NewRequest(http.MethodGet, "/notif", nil)
	w := httptest.NewRecorder()

	assert.NotPanics(t, func() {
		r.ServeHTTP(w, req)
	})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// ---------------------------------------------------------------------------
// DirectStreaming procHandle
// ---------------------------------------------------------------------------

func TestDirectStreaming_ProcHandle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupTestDB(t)
	account := &models.Account{Id: 1}

	// Build the same procHandle logic as DirectStreaming to test serialization.
	// We can't call the handler directly (it calls startSseService), so we
	// replicate the closure to exercise serializer.ToConversation path.
	procHandle := func(data any) (event, payload string, err error) {
		conversation := &models.Conversation{Id: 1}
		convs, err := serializer.ToConversation(conversation, account)
		if err != nil {
			return "", "", err
		}

		payloadBytes, err := json.Marshal(convs)
		if err != nil {
			return "", "", err
		}

		payload = string(payloadBytes)
		event = "conversation"

		return event, payload, nil
	}

	// ToConversation needs DB; with the test DB it should not panic.
	assert.NotPanics(t, func() {
		_, _, _ = procHandle("data")
	})
}
