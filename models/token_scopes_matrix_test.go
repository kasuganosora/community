package models

import "testing"

func TestMatrixScopesAreOptIn(t *testing.T) {
	s, _, ok := ParsePersonalTokenScopes([]string{"read", "write", "follow", "push"})
	if !ok || s.OtherScope&(AppScopeReadMatrix|AppScopeWriteMatrix) != 0 {
		t.Fatalf("umbrella scopes must not grant Matrix: %+v", s)
	}
	s, names, ok := ParsePersonalTokenScopes([]string{"write:matrix", "read:matrix"})
	if !ok || s.OtherScope != AppScopeReadMatrix|AppScopeWriteMatrix || s.ReadScope != 0 || s.WriteScope != 0 {
		t.Fatalf("matrix scopes: %+v", s)
	}
	if names[0] != "read:matrix" || names[1] != "write:matrix" {
		t.Fatalf("names %v", names)
	}
	if got := s.OAuthScopeString(); got != "read:matrix write:matrix" {
		t.Fatalf("scope string %q", got)
	}
}
