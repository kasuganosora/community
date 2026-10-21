package middleware

import "testing"

func TestLoginFailureGuardMemory(t *testing.T) {
	if GetRedisClient() != nil {
		t.Skip("redis configured")
	}
	if b, _ := LoginBlocked("Alice", "203.0.113.9"); b {
		t.Fatal("blocked before any failure")
	}
	for i := 0; i < LoginFailureLimit; i++ {
		RecordLoginFailure("alice", "203.0.113.9")
	}
	if b, retry := LoginBlocked("ALICE", "198.51.100.1"); !b || retry <= 0 {
		t.Fatal("account should be blocked from any IP")
	}
	if b, _ := LoginBlocked("bob", "203.0.113.9"); !b {
		t.Fatal("IP should be blocked for any account")
	}
	if b, _ := LoginBlocked("bob", "198.51.100.1"); b {
		t.Fatal("unrelated account/IP blocked")
	}
}
