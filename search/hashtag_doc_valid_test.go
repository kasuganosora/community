package search

import "testing"

func TestValidHashtagDocument(t *testing.T) {
	good := []*HashtagSearchDocument{
		{ID: "horror", Name: "horror"},
		{ID: HashtagDocumentID("你好"), Name: "你好"},
		{ID: "a-b_c", Name: "a-b_c"},
	}
	for _, d := range good {
		if !ValidHashtagDocument(d) {
			t.Errorf("expected valid: %+v", d)
		}
	}
	bad := []*HashtagSearchDocument{
		nil,
		{ID: "", Name: "x"},
		{ID: "x", Name: ""},
		{ID: "<span>horror</span></a></p>", Name: "<span>horror</span></a></p>"},
		{ID: HashtagDocumentID("<span>horror</span></a></p>"), Name: "<span>horror</span></a></p>"},
		{ID: "x", Name: "39;ll"},
		{ID: "x", Name: "a b"},
		{ID: "x", Name: "&#34;x"},
	}
	for _, d := range bad {
		if ValidHashtagDocument(d) {
			t.Errorf("expected invalid: %+v", d)
		}
	}
	docs := FilterHashtagDocuments(append(append([]*HashtagSearchDocument{}, good...), bad...))
	if len(docs) != len(good) {
		t.Fatalf("filtered %d docs, want %d", len(docs), len(good))
	}
}
