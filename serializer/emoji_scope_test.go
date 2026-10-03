package serializer

import (
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestStatusEmojiScopeIncludesCWAndRejectsOtherDomainAndHTMLAttributes(t *testing.T) {
	domain := "remote.example"
	origin := "https://remote.example/notes/1"
	status := &models.Status{Id: 1, Domain: &domain, OriginUri: &origin, Account: &models.Account{Username: "author"}, Content: models.StatusContent{Content: `<p>:party: <a href="https://example/:attribute:">link</a><code>:code:</code></p>`, SpoilerText: ":warning:"}, Emojis: []*models.Emoji{
		{Shortcode: "party", Domain: domain, Url: "https://remote.example/right.png"},
		{Shortcode: "party", Url: "https://local.example/wrong.png"},
		{Shortcode: "warning", Domain: domain},
		{Shortcode: "attribute", Domain: domain},
		{Shortcode: "code", Domain: domain},
	}}
	result, err := ToStatusBase(status)
	require.NoError(t, err)
	require.Len(t, result.Emojis, 2)
	require.Equal(t, "https://remote.example/right.png", result.Emojis[0].Url)
	require.Equal(t, "warning", result.Emojis[1].Shortcode)
}
func TestAccountEmojisOnlyUsedInProfile(t *testing.T) {
	domain := "remote.example"
	account := &models.Account{Domain: &domain, DisplayName: "Name :display:", Note: "<p>:bio:</p>", Extra: &models.AccountExtra{Fields: []models.AccountField{{Name: "key", Val: ":field:"}}}, Emojis: []*models.Emoji{
		{Shortcode: "display", Domain: domain}, {Shortcode: "bio", Domain: domain}, {Shortcode: "field", Domain: domain}, {Shortcode: "unrelated", Domain: domain}, {Shortcode: "display"},
	}}
	require.Len(t, ToAccount(account).Emoji, 3)
}
