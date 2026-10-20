package activitypub

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocationEmojiResolvedFromRemoteServer(t *testing.T) {
	old := fetchMisskeyEmoji
	t.Cleanup(func() { fetchMisskeyEmoji = old })
	var asked []string
	fetchMisskeyEmoji = func(_ context.Context, host, name string) (string, error) {
		asked = append(asked, host+"/"+name)
		if name == "fictional_town" {
			return "https://media.remote.example/emoji/fictional_town.png", nil
		}
		return "", nil
	}
	a := &Actor{ID: "https://remote.example/users/abc", VcardAddress: ":fictional_town: :in_tags: :missing:",
		Tag: []any{map[string]any{"type": "Emoji", "name": ":in_tags:", "icon": map[string]any{"url": "https://media.remote.example/e/in_tags.png"}}}}
	addLocationEmojiTags(context.Background(), a)
	require.ElementsMatch(t, []string{"remote.example/fictional_town", "remote.example/missing"}, asked)
	tags := decodeEmojiTags(a.Tag)
	require.Len(t, tags, 2)
	require.Equal(t, ":fictional_town:", tags[1].Name)
	require.Equal(t, "https://media.remote.example/emoji/fictional_town.png", tags[1].Icon.URL)

	// No location: nothing is fetched.
	asked = nil
	addLocationEmojiTags(context.Background(), &Actor{ID: "https://remote.example/users/x"})
	require.Empty(t, asked)
}
