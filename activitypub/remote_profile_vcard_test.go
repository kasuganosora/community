package activitypub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMisskeyVcardLocationAndBirthday(t *testing.T) {
	raw := `{"id":"https://remote.example/users/x","type":"Person","preferredUsername":"fictional",
		"vcard:Address":"  <b>:fictional_town:</b> ","vcard:bday":"0999-10-27"}`
	var a Actor
	require.NoError(t, json.Unmarshal([]byte(raw), &a))
	require.Equal(t, ":fictional_town:", remoteLocation(&a))
	require.Equal(t, "0999-10-27", remoteBirthday(&a))

	for _, bad := range []string{"", "10-27", "2024-13-40", "yesterday!"} {
		require.Empty(t, remoteBirthday(&Actor{VcardBday: bad}), bad)
	}
	require.Len(t, []rune(remoteLocation(&Actor{VcardAddress: strings.Repeat("あ", 300)})), 128)
	require.Empty(t, remoteLocation(nil))
}

func TestLongMisskeyEmojiShortcodesAreAccepted(t *testing.T) {
	long := ":" + strings.Repeat("a_fictional_long_name_", 4) + ":"
	require.Greater(t, len(long), 38)
	require.NotNil(t, emojiName.FindStringSubmatch(long))
	require.Nil(t, emojiName.FindStringSubmatch(":"+strings.Repeat("x", 129)+":"))
	require.NotNil(t, customReaction.FindStringSubmatch(long))
}
