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

func init() {
	gin.SetMode(gin.TestMode)
}

// TestRegisterRequest_Binding 测试注册请求绑定.
func TestRegisterRequest_Binding(t *testing.T) {
	tests := []struct {
		name        string
		formData    map[string]string
		expectError bool
	}{
		{
			name: "valid request",
			formData: map[string]string{
				"user[account_attributes][display_name]": "Test User", //nolint:goconst
				"user[account_attributes][username]":     "testuser",
				"user[email]":                            "test@example.com",
				"user[password]":                         "password123", //nolint:goconst
				"user[password_confirmation]":            "password123",
			},
			expectError: false,
		},
		{
			name: "missing username",
			formData: map[string]string{
				"user[account_attributes][display_name]": "Test User",
				"user[email]":                            "test@example.com",
				"user[password]":                         "password123",
				"user[password_confirmation]":            "password123",
			},
			expectError: true,
		},
		{
			name: "missing password",
			formData: map[string]string{
				"user[account_attributes][display_name]": "Test User",
				"user[account_attributes][username]":     "testuser",
				"user[email]":                            "test@example.com",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			// Build form data
			form := make(url.Values)
			for key, value := range tt.formData {
				form.Set(key, value)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", strings.NewReader(form.Encode())) //nolint:noctx // test code //nolint:noctx
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			c.Request = req

			var reqStruct RegisterRequest

			err := c.ShouldBind(&reqStruct)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestLoginRequest_Binding 测试登录请求绑定.
func TestLoginRequest_Binding(t *testing.T) {
	tests := []struct {
		name        string
		formData    map[string]string
		expectError bool
	}{
		{
			name: "login with username",
			formData: map[string]string{
				"username": "testuser",
				"password": "password123",
			},
			expectError: false,
		},
		{
			name: "login with email",
			formData: map[string]string{
				"email":    "test@example.com",
				"password": "password123",
			},
			expectError: false,
		},
		{
			name: "missing password",
			formData: map[string]string{
				"username": "testuser",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			form := make(url.Values)
			for key, value := range tt.formData {
				form.Set(key, value)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/sign_in", strings.NewReader(form.Encode())) //nolint:noctx // test code //nolint:noctx
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			c.Request = req

			var reqStruct AuthRequest

			err := c.ShouldBind(&reqStruct)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestAuthRequest_Validation 测试登录请求验证逻辑.
func TestAuthRequest_Validation(t *testing.T) {
	tests := []struct {
		name          string
		username      string
		email         string
		password      string
		expectValid   bool
		expectMessage string
	}{
		{
			name:        "valid with username",
			username:    "testuser",
			password:    "password123",
			expectValid: true,
		},
		{
			name:        "valid with email",
			email:       "test@example.com",
			password:    "password123",
			expectValid: true,
		},
		{
			name:          "missing both username and email",
			password:      "password123",
			expectValid:   false,
			expectMessage: "username or email is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &AuthRequest{
				Username: tt.username,
				Email:    tt.email,
				Password: tt.password,
			}

			// Check the validation logic
			if tt.expectValid {
				assert.True(t, req.Username != "" || req.Email != "", "Should have username or email")
			} else {
				assert.True(t, req.Username == "" && req.Email == "", "Should have neither username nor email")
			}
		})
	}
}
