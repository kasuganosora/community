package account

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateRegisterRequest(t *testing.T) {
	ok := RegisterRequest{DisplayName: "Alice", UserName: "alice_01", Email: "alice@example.com", Password: "Passw0rd!123"}

	mod := func(f func(r *RegisterRequest)) RegisterRequest {
		r := ok
		f(&r)

		return r
	}

	tests := []struct {
		name  string
		req   RegisterRequest
		valid bool
	}{
		{"valid", ok, true},
		{"cjk username", mod(func(r *RegisterRequest) { r.UserName = "中文用户" }), true},
		{"path traversal", mod(func(r *RegisterRequest) { r.UserName = "../../etc" }), false},
		{"slash", mod(func(r *RegisterRequest) { r.UserName = "a/b" }), false},
		{"at sign", mod(func(r *RegisterRequest) { r.UserName = "a@b" }), false},
		{"space", mod(func(r *RegisterRequest) { r.UserName = "a b" }), false},
		{"maximum username length", mod(func(r *RegisterRequest) { r.UserName = strings.Repeat("a", 20) }), true},
		{"too long username", mod(func(r *RegisterRequest) { r.UserName = strings.Repeat("a", 21) }), false},
		{"maximum unicode username length", mod(func(r *RegisterRequest) { r.UserName = strings.Repeat("名", 20) }), true},
		{"too long unicode username", mod(func(r *RegisterRequest) { r.UserName = strings.Repeat("名", 21) }), false},
		{"bad email", mod(func(r *RegisterRequest) { r.Email = "notanemail" }), false},
		{"email with name", mod(func(r *RegisterRequest) { r.Email = "Bob <bob@example.com>" }), false},
		{"short password", mod(func(r *RegisterRequest) { r.Password = "1" }), false},
		{"long display name", mod(func(r *RegisterRequest) { r.DisplayName = strings.Repeat("名", 65) }), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := validateRegisterRequest(&tt.req)
			if tt.valid {
				assert.Empty(t, msg)
			} else {
				assert.NotEmpty(t, msg)
			}
		})
	}
}
