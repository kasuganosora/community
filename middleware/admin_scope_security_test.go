package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func TestAdminScopeRequiresCurrentAdminIdentity(t *testing.T) {
	for _, tt := range []struct {
		name                           string
		web, admin, scope, missingUser bool
		want                           int
	}{
		{"ordinary web user", true, false, false, false, 403},
		{"admin web user", true, true, false, false, 204},
		{"oauth scope cannot elevate ordinary user", false, false, true, false, 403},
		{"oauth admin still needs scope", false, true, false, false, 403},
		{"oauth admin and scope", false, true, true, false, 204},
		{"oauth scope without user", false, false, true, true, 401},
	} {
		t.Run(tt.name, func(t *testing.T) {
			token := &models.AuthTokenData{AuthType: models.AuthTokenTypeOauth}
			if tt.web {
				token.AuthType = models.AuthTokenTypeWeb
			}
			if tt.scope {
				token.Scopes = &models.ApplicationScope{AdminScope: models.AppScopeAdminWrite}
			}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set("token_data", token)
				if !tt.missingUser {
					c.Set("user", &models.User{IsAdmin: tt.admin})
				}
			})
			r.POST("/admin/custom_emojis", CheckScopeMiddleware(Scope{Type: models.AppScopeTypeAdmin, Value: models.AppScopeAdminWrite}), func(c *gin.Context) { c.Status(204) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("POST", "/admin/custom_emojis", nil))
			require.Equal(t, tt.want, w.Code)
		})
	}
}
