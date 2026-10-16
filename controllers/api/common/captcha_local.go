package common

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/kasuganosora/community/captcha"
	"github.com/kasuganosora/community/captcha/local"
)

const captchaBindCookie = "captcha_bind"

func localCaptchaOpen(c *gin.Context) bool {
	if captcha.IsCaptchaEnabled() && captcha.GetCurrentProvider() == "local" {
		return true
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "The captcha has expired, please get a new one"})
	return false
}

type localVerifyRequest struct {
	Answer   string `json:"answer"`
	Nonce    string `json:"nonce"`
	Homepage string `json:"homepage"`
}

// LocalCaptchaStart opens a built-in challenge.
// POST /api/v1/captcha/local/start
func LocalCaptchaStart(c *gin.Context) {
	if !localCaptchaOpen(c) {
		return
	}
	started, err := local.Start(c.Request.Context(), c.ClientIP())
	if errors.Is(err, local.ErrRateLimited) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Captcha refreshed too often, please try again later"})
		return
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "The captcha is temporarily unavailable, please try again later"})
		return
	}
	secure := c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https"
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(captchaBindCookie, started.Bind, int((5*time.Minute)/time.Second), "/api/v1/captcha", "", secure, true)
	c.JSON(http.StatusOK, gin.H{
		"id":       started.ID,
		"total":    local.Total,
		"prompt":   started.Prompt,
		"pow_salt": started.PowSalt,
		"pow_bits": started.PowBits,
	})
}

// LocalCaptchaFrame returns the next animated character, or how long to wait.
// GET /api/v1/captcha/local/:id/frame
func LocalCaptchaFrame(c *gin.Context) {
	if !localCaptchaOpen(c) {
		return
	}
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "The captcha has expired, please get a new one"})
		return
	}
	bind, _ := c.Cookie(captchaBindCookie)
	result, err := local.Frame(c.Request.Context(), id, bind)
	if errors.Is(err, local.ErrInvalid) {
		c.JSON(http.StatusNotFound, gin.H{"error": "The captcha has expired, please get a new one"})
		return
	}
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "The captcha is temporarily unavailable, please try again later"})
		return
	}
	if len(result.GIF) == 0 {
		status := http.StatusAccepted
		if result.Done {
			status = http.StatusOK
		}
		c.Header("Cache-Control", "no-store")
		c.JSON(status, gin.H{
			"wait_ms": result.Wait.Milliseconds(),
			"index":   result.Index,
			"total":   local.Total,
			"done":    result.Done,
		})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Captcha-Index", strconv.Itoa(result.Index))
	c.Header("X-Captcha-Total", strconv.Itoa(local.Total))
	c.Header("X-Captcha-Wait-Ms", strconv.FormatInt(result.Wait.Milliseconds(), 10))
	if result.Done {
		c.Header("X-Captcha-Done", "1")
	} else {
		c.Header("X-Captcha-Done", "0")
	}
	c.Data(http.StatusOK, "image/gif", result.GIF)
}

// LocalCaptchaVerify checks the watched characters and returns a one-time token.
// POST /api/v1/captcha/local/:id/verify
func LocalCaptchaVerify(c *gin.Context) {
	if !localCaptchaOpen(c) {
		return
	}
	id := c.Param("id")
	if _, err := uuid.Parse(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "The captcha has expired, please get a new one"})
		return
	}
	var req localVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "Please watch all five captcha characters first"})
		return
	}
	bind, _ := c.Cookie(captchaBindCookie)
	token, err := local.Verify(c.Request.Context(), id, bind, req.Answer, req.Homepage, req.Nonce)
	switch {
	case errors.Is(err, local.ErrEarly):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "Please watch all five captcha characters first"})
	case errors.Is(err, local.ErrPow):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "Please wait, the captcha is still being confirmed"})
	case errors.Is(err, local.ErrWrong):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "The captcha is incorrect, please try again"})
	case errors.Is(err, local.ErrInvalid):
		c.JSON(http.StatusNotFound, gin.H{"error": "The captcha has expired, please get a new one"})
	case err != nil:
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "The captcha is temporarily unavailable, please try again later"})
	default:
		c.JSON(http.StatusOK, gin.H{"token": token})
	}
}
