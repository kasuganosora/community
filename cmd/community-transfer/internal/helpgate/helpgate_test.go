package helpgate

import "testing"

func TestRequested(t *testing.T) {
	for _, c := range []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{"help"}, true},
		{[]string{"-h"}, true},
		{[]string{"--help"}, true},
		{[]string{"-help"}, true},
		{[]string{"--input", "x", "--help"}, true},
		{[]string{"--input", "x"}, false},
		{[]string{"--", "--help"}, false},
		{[]string{"--input", "help"}, false},
	} {
		if got := Requested(c.args); got != c.want {
			t.Errorf("Requested(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}
