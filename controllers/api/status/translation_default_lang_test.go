package status

import (
	"testing"

	"github.com/kasuganosora/community/models"
)

func TestDefaultTargetLang(t *testing.T) {
	cases := []struct {
		acct   *models.Account
		accept string
		want   string
	}{
		{&models.Account{Language: "ja"}, "de-DE,de;q=0.9", "ja"},
		{&models.Account{}, "de-DE,de;q=0.9", "de-DE"},
		{nil, "fr;q=0.8", "fr"},
		{nil, "*", "en"},
		{nil, "", "en"},
	}
	for _, c := range cases {
		if got := defaultTargetLang(c.acct, c.accept); got != c.want {
			t.Errorf("%+v %q: got %q want %q", c.acct, c.accept, got, c.want)
		}
	}
}
