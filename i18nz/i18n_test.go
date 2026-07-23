package i18nz //nolint:goconst // acceptable string literal usage

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetLocalizer(t *testing.T) {
	tests := []struct {
		name   string
		lang   string
		accept string
	}{
		{"empty lang and accept", "", ""},
		{"with lang only", "en", ""},
		{"with accept only", "", "en-US,en;q=0.9"}, //nolint:goconst
		{"with both", "zh", "en-US,en;q=0.9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := GetLocalizer(tt.lang, tt.accept)
			assert.NotNil(t, loc)
		})
	}
}

func TestGetMailTemplateName(t *testing.T) {
	tests := []struct {
		name         string
		lang         string
		templateName string
	}{
		{"english template", "en", "welcome"}, //nolint:goconst
		{"chinese template", "zh", "welcome"},
		{"japanese template", "ja", "welcome"},
		{"empty lang", "", "welcome"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetMailTemplateName(tt.lang, tt.templateName)
			assert.NotEmpty(t, result)
		})
	}
}

func TestBundle(t *testing.T) {
	assert.NotNil(t, Bundle)
}

func TestOStruct(t *testing.T) {
	o := O{
		Key:   "PluralCount",
		Value: "5",
	}
	assert.Equal(t, "PluralCount", o.Key)
	assert.Equal(t, "5", o.Value)
}

func TestLocalizerStruct(t *testing.T) {
	loc := &Localizer{}
	assert.NotNil(t, loc)
}

func TestLocalizer_DifferentLanguages(t *testing.T) {
	languages := []string{"en", "zh", "ja", "ko", "es", "fr", "de"}

	for _, lang := range languages {
		t.Run("language_"+lang, func(t *testing.T) {
			loc := GetLocalizer(lang, "")
			assert.NotNil(t, loc)
		})
	}
}

func TestLocalizer_AcceptLanguageParsing(t *testing.T) {
	acceptHeaders := []string{
		"en-US,en;q=0.9",
		"zh-CN,zh;q=0.9,en;q=0.8",
		"ja,en-US;q=0.9,en;q=0.8",
		"*",
		"",
	}

	for _, accept := range acceptHeaders {
		t.Run("accept_"+accept, func(t *testing.T) {
			loc := GetLocalizer("", accept)
			assert.NotNil(t, loc)
		})
	}
}

func TestIsTestMode(t *testing.T) {
	// In test mode, this should return true
	assert.True(t, isTestMode())
}

func TestGetMailTemplateName_DefaultLang(t *testing.T) {
	result := GetMailTemplateName("", "welcome")
	assert.Contains(t, result, "welcome")
}

func TestOStruct_ZeroValues(t *testing.T) {
	o := O{}
	assert.Empty(t, o.Key)
	assert.Empty(t, o.Value)
}
