package middleware

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAuthMiddleware(t *testing.T) {
	// AuthMiddleware returns a gin.HandlerFunc
	handler := AuthMiddleware()
	assert.NotNil(t, handler)
}

func TestCheckAuthMiddleware(t *testing.T) {
	// CheckAuthMiddleware returns a gin.HandlerFunc
	handler := CheckAuthMiddleware()
	assert.NotNil(t, handler)
}

func TestScopeStruct(t *testing.T) {
	scope := Scope{
		Type:  1,
		Value: 100,
	}

	assert.Equal(t, uint8(1), scope.Type)
	assert.Equal(t, uint64(100), scope.Value)
}

func TestCheckScopeMiddleware(t *testing.T) {
	scopes := []Scope{
		{Type: 1, Value: 100},
		{Type: 2, Value: 200},
	}

	handler := CheckScopeMiddleware(scopes...)
	assert.NotNil(t, handler)
}

func TestCheckScopeMiddleware_EmptyScopes(t *testing.T) {
	handler := CheckScopeMiddleware()
	assert.NotNil(t, handler)
}

func TestGzip(t *testing.T) {
	handler := Gzip()
	assert.NotNil(t, handler)
}

func TestHandleErrorsMiddleware(t *testing.T) {
	handler := HandleErrorsMiddleware()
	assert.NotNil(t, handler)
}

func TestInternalApiMiddleware(t *testing.T) {
	handler := InternalApiMiddleware()
	assert.NotNil(t, handler)
}

func TestScope_RequiredScopes(t *testing.T) {
	scopes := []Scope{
		{Type: 1, Value: 1},
		{Type: 1, Value: 2},
		{Type: 2, Value: 1},
		{Type: 2, Value: 2},
	}

	assert.Len(t, scopes, 4)
	assert.Equal(t, uint8(1), scopes[0].Type)
	assert.Equal(t, uint8(1), scopes[1].Type)
}

func TestAuthMiddleware_ReturnsGinHandler(t *testing.T) {
	handler := AuthMiddleware()

	// Verify it's a valid function
	assert.NotNil(t, handler)
}

func TestScope_ZeroValues(t *testing.T) {
	scope := Scope{}

	assert.Equal(t, uint8(0), scope.Type)
	assert.Equal(t, uint64(0), scope.Value)
}
