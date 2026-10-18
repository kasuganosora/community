package datatransfer

import (
	"encoding/json"
	"testing"
)

func TestMediaAcceptsFloatDimensions(t *testing.T) {
	var m Media
	if err := json.Unmarshal([]byte(`{"id":"f1","size":12345.0,"width":1665.517,"height":"936.4","name":"a.png"}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Width != 1666 || m.Height != 936 || m.Size != 12345 || m.ID != "f1" || m.Name != "a.png" {
		t.Fatalf("got %+v", m)
	}
	var b struct{ Media []Media }
	if err := json.Unmarshal([]byte(`{"Media":[{"id":"x","width":null}]}`), &b); err != nil || b.Media[0].Width != 0 {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"width":"abc"}`), &m); err == nil {
		t.Fatal("want error for non-numeric width")
	}
}
