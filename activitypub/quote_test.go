package activitypub

import "testing"

func TestQuoteObjectURI(t *testing.T) {
	cases := []struct {
		obj  map[string]any
		want string
	}{
		{map[string]any{"quote": "https://a.example/s/1"}, "https://a.example/s/1"},
		{map[string]any{"quote": map[string]any{"id": "https://a.example/s/2"}}, "https://a.example/s/2"},
		{map[string]any{"_misskey_quote": "https://m.example/notes/x", "quoteUrl": "https://m.example/notes/x"}, "https://m.example/notes/x"},
		{map[string]any{"quoteUri": "https://p.example/o/3"}, "https://p.example/o/3"},
		{map[string]any{"quote": 3}, ""},
		{map[string]any{}, ""},
	}
	for _, c := range cases {
		if got := quoteObjectURI(c.obj); got != c.want {
			t.Errorf("quoteObjectURI(%v) = %q, want %q", c.obj, got, c.want)
		}
	}
}
