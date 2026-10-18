package main

import "testing"

func TestGuardedRunConvertsPanics(t *testing.T) {
	if code := guardedRun(func() int { panic("no redis config") }); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
	if code := guardedRun(func() int { return 3 }); code != 3 {
		t.Fatalf("code = %d, want 3", code)
	}
}
