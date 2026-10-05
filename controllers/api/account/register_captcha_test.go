package account

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestCaptchaTokenReadsWidgetFields(t *testing.T) {
	gin.SetMode(gin.TestMode)

	assert.Equal(t, "bound", captchaToken(newCaptchaContext(t, url.Values{}), " bound "))

	h := url.Values{}
	h.Set("h-captcha-response", "from-hcaptcha")
	h.Set("cf-turnstile-response", "from-turnstile")
	assert.Equal(t, "from-hcaptcha", captchaToken(newCaptchaContext(t, h), ""))

	turnstile := url.Values{}
	turnstile.Set("cf-turnstile-response", "from-turnstile")
	assert.Equal(t, "from-turnstile", captchaToken(newCaptchaContext(t, turnstile), "  "))

	generic := url.Values{}
	generic.Set("captcha_response", "generic")
	assert.Equal(t, "generic", captchaToken(newCaptchaContext(t, generic), ""))
}

func newCaptchaContext(t *testing.T, form url.Values) *gin.Context {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	return c
}
