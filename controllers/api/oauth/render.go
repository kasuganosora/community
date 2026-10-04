package oauth

import (
	"bytes"
	"html/template"
	"io/fs"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/internalfs"
)

type consentView struct {
	Title       string
	Domain      string
	AppName     string
	Website     string
	Icon        string
	DisplayName string
	Username    string
	Initial     string
	Labels      []string
	Action      string
	Hidden      [][2]string
}

func renderConsent(c *gin.Context, view consentView) {
	if view.Initial == "" {
		view.Initial = initial(view.DisplayName)
	}

	if view.DisplayName == "" {
		view.DisplayName = view.Username
	}

	body, err := fs.ReadFile(internalfs.Get(), "oauth_authorize.html")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "authorization page unavailable"})

		return
	}

	tpl, err := template.New("oauth_authorize.html").Parse(string(body))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "authorization page unavailable"})

		return
	}

	var buf bytes.Buffer
	if err = tpl.Execute(&buf, view); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "authorization page unavailable"})

		return
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", buf.Bytes())
}

func renderOOB(c *gin.Context, code, appName string, auto bool) {
	body, err := fs.ReadFile(internalfs.Get(), "oauth_oob.html")
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "authorization page unavailable"})

		return
	}

	tpl, err := template.New("oauth_oob.html").Parse(string(body))
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "authorization page unavailable"})

		return
	}

	var buf bytes.Buffer
	if err = tpl.Execute(&buf, map[string]any{
		"Code":    code,
		"AppName": appName,
		"Auto":    auto,
		"Domain":  issuer(),
	}); err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "authorization page unavailable"})

		return
	}

	c.Data(http.StatusOK, "text/html; charset=utf-8", buf.Bytes())
}

func initial(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "?"
	}

	r, _ := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		return "?"
	}

	return string(r)
}

func hiddenPairs(values map[string]string, keys ...string) [][2]string {
	out := make([][2]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, [2]string{key, values[key]})
	}

	return out
}
