package serializer

import "testing"

func TestStillAvatarURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://c.test/proxy/media/tok":          "https://c.test/proxy/media/tok?static=1",
		"https://c.test/proxy/media/tok?x=1":      "https://c.test/proxy/media/tok?x=1&static=1",
		"https://c.test/proxy/media/tok?static=1": "https://c.test/proxy/media/tok?static=1",
		"https://c.test/media/local.png":          "https://c.test/media/local.png",
	} {
		if got := stillAvatarURL(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}
