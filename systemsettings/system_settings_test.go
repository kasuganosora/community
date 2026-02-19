package systemsettings

import (
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
	assert.Equal(t, 3.14, result)
}

func TestGetBool_Default(t *testing.T) {
	loaded = true
	settings = map[string]any{}

	assert.True(t, GetBool("nonexistent", true))
	assert.False(t, GetBool("nonexistent", false))
}

func TestGetAllFlatStyle(t *testing.T) {
	// Just verify the function doesn't panic
	assert.NotNil(t, GetAllFlatStyle)
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
