package systemsettings

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, "domain", KeyDomain)
	assert.Equal(t, "title", KeyTitle)
	assert.Equal(t, "description", KeyDescription)
	assert.Equal(t, "thumbnail", KeyThumbnail)
	assert.Equal(t, "languages", KeyLanguages)
	assert.Equal(t, "config.accounts.max_featured_tags", KeyAccountMaxFeaturedTags)
	assert.Equal(t, "config.statuses.max_characters", KeyStatusMaxCharacters)
	assert.Equal(t, "config.statuses.max_media_attachments", KeyStatusMaxMedia)
	assert.Equal(t, "config.registration.enabled", KeyIsEnableRegistration)
	assert.Equal(t, "config.recaptcha.enabled", KeyRecaptchaEnabled)
}

func TestSet(t *testing.T) {
	// Reset
	loaded = true
	settings = map[string]any{}

	Set("test_key", "test_value")
	assert.Equal(t, "test_value", settings["test_key"])
}

func TestGetString_FromSettings(t *testing.T) {
	loaded = true
	settings = map[string]any{}
	settings["string_key"] = "hello"

	result := GetString("string_key", "default")
	assert.Equal(t, "hello", result)
}

func TestGetString_Default(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	result := GetString("nonexistent", "default_value")
	assert.Equal(t, "default_value", result)
}

func TestGetInt_Default(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	result := GetInt("nonexistent", 100)
	assert.Equal(t, 100, result)
}

func TestGetInt64_Default(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	result := GetInt64("nonexistent", int64(100))
	assert.Equal(t, int64(100), result)
}

func TestGetFloat64_Default(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	result := GetFloat64("nonexistent", 3.14)
	assert.InEpsilon(t, 3.14, result, 0.001)
}

func TestGetBool_Default(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	assert.True(t, GetBool("nonexistent", true))
	assert.False(t, GetBool("nonexistent", false))
}

func TestGetAllFlatStyle(t *testing.T) {
	loaded = true
	settings = map[string]any{
		"key1":           "value1",
		"config.enabled": true,
		"nested.a.b.c":   123,
	}

	result := GetAllFlatStyle()
	// Regression: old implementation had a named-return shadowing bug that
	// always returned nil instead of the cached settings.
	assert.NotNil(t, result)
	assert.Len(t, result, 3)
	assert.Equal(t, "value1", result["key1"])
	assert.Equal(t, true, result["config.enabled"])
	assert.Equal(t, 123, result["nested.a.b.c"])

	// Returned map must be a defensive copy; mutating it must not affect
	// the internal cache.
	result["key1"] = "mutated"
	assert.NotEqual(t, "mutated", settings["key1"])
}

func TestSetAndGet(t *testing.T) {
	loaded = true // Prevent loading from DB
	settings = map[string]any{}

	Set("new_key", "new_value")

	result := GetString("new_key", "")
	assert.Equal(t, "new_value", result)
}

func TestGetIntsWithNil(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	result := GetInts("nonexistent", nil)
	assert.Nil(t, result)
}

func TestGetInt64sWithNil(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	result := GetInt64s("nonexistent", nil)
	assert.Nil(t, result)
}

func TestGetStringsWithNil(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	result := GetStrings("nonexistent", nil)
	assert.Nil(t, result)
}

func TestGetMapWithNil(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	result := GetMap("nonexistent", nil)
	assert.Nil(t, result)
}

func TestGetAllTreeStyle(t *testing.T) {
	loaded = true
	settings = map[string]any{
		"config":         "scalar", // leaf at "config"
		"config.enabled": true,     // ... but also nested under config.enabled
		"server.host":    "localhost",
		"server.port":    8080,
		"a.b.c.d":        "deep",
	}

	// Must not panic even when a scalar value conflicts with a subtree path.
	result := GetAllTreeStyle()
	assert.NotNil(t, result)

	// config should be a map (the later, deeper key wins the intermediate node)
	configMap, ok := result["config"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, true, configMap["enabled"])

	serverMap, ok := result["server"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "localhost", serverMap["host"])
	assert.Equal(t, 8080, serverMap["port"])

	aMap, ok := result["a"].(map[string]any)
	assert.True(t, ok)
	bMap, ok := aMap["b"].(map[string]any)
	assert.True(t, ok)
	cMap, ok := bMap["c"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "deep", cMap["d"])
}

func TestGetAllTreeStyle_NoPanicOnConflict(t *testing.T) {
	// Regression: old implementation did `ret = ret[keyParts[i]].(map[string]any)`
	// which panics when a leaf value occupies an intermediate key path.
	assert.NotPanics(t, func() {
		loaded = true
		settings = map[string]any{
			"a":     1,
			"a.b":   2,
			"a.b.c": 3,
		}
		_ = GetAllTreeStyle()
	})
}

func TestSaveToDB_NilDB(t *testing.T) {
	// Regression: old implementation called db.GetDB().Model(...) directly and
	// would panic with a nil pointer dereference when the DB is unavailable
	// (standalone/test mode). It must now return a clean error instead.
	assert.NotPanics(t, func() {
		err := SaveToDB("some_key")
		assert.Error(t, err)
	})
}

func TestSaveToDB_EmptyKeys(t *testing.T) {
	// No keys -> nothing to do, must be a no-op without hitting the DB.
	err := SaveToDB()
	assert.NoError(t, err)
}

// TestConcurrentReadWrite verifies that concurrent readers (Get) and a single
// writer (Set) do not trigger a Go data race. Run with `go test -race`.
func TestConcurrentReadWrite(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	var wg sync.WaitGroup

	// Writer goroutine: continuously updates settings
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			Set("concurrent_key", "value")
		}
	}()

	// Reader goroutines: continuously read settings
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				_ = Get("concurrent_key", "default")
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, "value", Get("concurrent_key", "default"))
}

// TestConcurrentSetAndSaveToDB verifies that concurrent Set() and SaveToDB()
// don't trigger a data race. SaveToDB reads the settings map internally, so
// it must be synchronized with writers. Run with `go test -race`.
func TestConcurrentSetAndSaveToDB(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	var wg sync.WaitGroup

	// Concurrent writers
	for range 4 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := range 50 {
				Set(fmt.Sprintf("key_%d_%d", id, i), i)
			}
		}(0)
	}

	// SaveToDB will fail because DB is nil (test mode), but the important
	// part is that it must take the read lock before touching the map.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 50 {
			_ = SaveToDB("key_0_0")
		}
	}()

	wg.Wait()
}
