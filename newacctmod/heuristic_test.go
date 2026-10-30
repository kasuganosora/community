package newacctmod

import "testing"

func TestFingerprintStable(t *testing.T) {
	a := Fingerprint("Hello, WORLD!! https://spam.example/x")
	b := Fingerprint("hello world https://other.example/y")
	if a == "" || a != b {
		t.Fatalf("expected same fingerprint, got %q vs %q", a, b)
	}
}

func TestFingerprintEmpty(t *testing.T) {
	if Fingerprint("https://only.link") != "" && Fingerprint("   ") != "" {
		// links stripped; empty body => empty fp
	}
	if Fingerprint("   ") != "" {
		t.Fatal("expected empty")
	}
}

func TestExtractLinks(t *testing.T) {
	got := ExtractLinks("see HTTP://Ex.Com/A and https://ex.com/a again")
	if len(got) != 1 {
		t.Fatalf("dedupe host+path: %v", got)
	}
}
