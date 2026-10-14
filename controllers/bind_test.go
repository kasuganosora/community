package controllers

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

type bindInner struct {
	Options   []string `json:"options"`
	ExpiresIn uint64   `json:"expires_in"`
	Multiple  bool     `json:"multiple"`
}
type bindOuter struct {
	MediaIDs []string   `json:"media_ids"`
	Poll     *bindInner `json:"poll"`
	Flag     *bool      `form:"flag"`
	PathID   uint64     `form:"-" json:"path_id"`
	Fields   []struct {
		Name string `json:"name"`
	} `json:"fields_attributes"`
}

func TestDecodeFormValuesRailsKeys(t *testing.T) {
	var out bindOuter
	v, _ := url.ParseQuery("media_ids[]=1&media_ids[]=2&poll[options][]=a&poll[options][]=b&poll[expires_in]=60&poll[multiple]=true&path_id=9&fields_attributes[0][name]=x&fields_attributes[1][name]=y")
	v.Set("flag", "on")
	require.NoError(t, DecodeFormValues(v, &out))
	require.Equal(t, []string{"1", "2"}, out.MediaIDs)
	require.Equal(t, []string{"a", "b"}, out.Poll.Options)
	require.EqualValues(t, 60, out.Poll.ExpiresIn)
	require.True(t, out.Poll.Multiple)
	require.True(t, *out.Flag)
	require.Zero(t, out.PathID)
	require.Len(t, out.Fields, 2)
	require.Equal(t, "y", out.Fields[1].Name)
}
