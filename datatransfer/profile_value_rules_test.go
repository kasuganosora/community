package datatransfer

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The Misskey value rules are shared with scripts/migration/export_misskey.py
// and the verify / compare scripts: both sides run these vectors.
func TestMisskeyValueRuleVectors(t *testing.T) {
	raw, err := os.ReadFile("../scripts/migration/testdata/misskey_value_rules.json")
	require.NoError(t, err)
	var v struct {
		AlsoKnownAs []struct {
			Name       string   `json:"name"`
			SourceOnly bool     `json:"source_only"`
			Raw        any      `json:"raw"`
			Split      []string `json:"split"`
			Valid      []string `json:"valid"`
		} `json:"also_known_as"`
		Birthday struct {
			Now   time.Time `json:"now"`
			Cases []struct {
				Raw   string `json:"raw"`
				Valid string `json:"valid"`
			} `json:"cases"`
		} `json:"birthday"`
	}
	require.NoError(t, json.Unmarshal(raw, &v))
	require.NotEmpty(t, v.AlsoKnownAs)
	require.NotEmpty(t, v.Birthday.Cases)
	for _, c := range v.AlsoKnownAs {
		if c.SourceOnly {
			continue
		}
		var in []string
		switch x := c.Raw.(type) {
		case string:
			in = []string{x}
		case []any:
			for _, e := range x {
				in = append(in, e.(string))
			}
		}
		split := splitAlsoKnownAs(in)
		require.Equal(t, len(c.Split), len(split), c.Name)
		if len(c.Split) > 0 {
			require.Equal(t, c.Split, split, c.Name)
		}
		var valid []string
		for _, u := range split {
			if validActorURI(u) {
				valid = append(valid, u)
			}
		}
		require.Equal(t, len(c.Valid), len(valid), c.Name)
		if len(c.Valid) > 0 {
			require.Equal(t, c.Valid, valid, c.Name)
		}
	}
	for _, c := range v.Birthday.Cases {
		got, ok := validBirthday(c.Raw, v.Birthday.Now)
		require.Equal(t, c.Valid, got, c.Raw)
		require.Equal(t, c.Valid != "", ok, c.Raw)
	}
}
