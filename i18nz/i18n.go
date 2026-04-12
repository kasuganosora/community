package i18nz

import (
	"os"
	"strings"

	"github.com/kasuganosora/community/internalfs"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/pelletier/go-toml/v2"
	"golang.org/x/text/language"
)

var Bundle *i18n.Bundle

func init() {
	Bundle = i18n.NewBundle(language.English)
	Bundle.RegisterUnmarshalFunc("toml", toml.Unmarshal)

	// Skip i18n loading in test mode
	if isTestMode() {
		return
	}

	_, err := Bundle.LoadMessageFileFS(internalfs.Get(), "i18n/en.toml")
	if err != nil {
		panic(err)
	}
}

// isTestMode detects if we're running in test mode
func isTestMode() bool {
	// Check command line arguments
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "-test.") {
			return true
		}
	}
	// Check environment variable
	if os.Getenv("GO_TEST_MODE") == "1" {
		return true
	}
	return false
}

func GetLocalizer(lang string, accept string) *Localizer {
	langs := make([]string, 0)
	if accept != "" {
		tag, _ := language.MatchStrings(language.NewMatcher(Bundle.LanguageTags()), accept)
		langs = append(langs, tag.String())
	}

	if lang != "" {
		langs = append(langs, lang)
	}

	return &Localizer{*i18n.NewLocalizer(Bundle, langs...)}
}

type O struct {
	Key   string
	Value string
}

type Localizer struct {
	i18n.Localizer
}

func (l *Localizer) T(id string, data map[string]any, options ...O) string {
	config := &i18n.LocalizeConfig{
		MessageID:    id,
		TemplateData: data,
	}

	for _, option := range options {
		switch option.Key {
		case "PluralCount":
			config.PluralCount = option.Value
		}
	}

	return l.MustLocalize(config)
}

func GetMailTemplateName(lang string, templateName string) string {
	if lang == "" {
		lang = "en"
	}
	langTag := language.Make(lang)

	fs := internalfs.Get()
	fullTemplateName := "mails/" + templateName + "_" + langTag.String() + ".html"

	f, err := fs.Open(fullTemplateName)
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	if err == nil {
		return templateName + "_" + lang
	}

	// 否则返回默认模板
	return templateName + "_" + "en"
}
