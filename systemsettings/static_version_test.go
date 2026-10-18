package systemsettings

import "testing"

func TestVersionedStatic(t *testing.T) {
	t.Cleanup(func() { SetStaticVersion("") })
	SetStaticVersion("")
	if got := VersionedStatic("/logo.svg"); got != "/logo.svg" {
		t.Fatalf("no version: %q", got)
	}
	SetStaticVersion("568cd1c")
	if got := VersionedStatic("/icons/icon-192.png"); got != "/icons/icon-192.png?v=568cd1c" {
		t.Fatalf("got %q", got)
	}
}
