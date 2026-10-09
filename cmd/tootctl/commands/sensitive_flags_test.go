package commands

import (
	"bytes"
	"testing"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func runStatuses(args ...string) (string, error) {
	var out bytes.Buffer
	app := &cli.App{Name: "tootctl", Commands: []*cli.Command{StatusesCommand()}, Writer: &out, ErrWriter: &out}
	err := app.Run(append([]string{"tootctl", "statuses"}, args...))

	return out.String(), err
}

// A remote status imported without statuses.domain is still refreshed, and
// the domain is repaired from its author.
func TestRefreshSensitiveHandlesNullDomain(t *testing.T) {
	conn := setupAccountsTest(t)
	require.NoError(t, conn.AutoMigrate(&models.Status{}, &models.StatusContent{}, &models.MediaAttachment{}, &models.DomainBlock{}))
	remoteAccount(t, conn, 11, "rururoriri", "misskey.io", false)
	uri := "https://misskey.io/notes/as3wmlwh74m9063a"
	st := models.Status{Id: 422, AccountId: 11, OriginUri: &uri}
	require.NoError(t, conn.Omit("Content", "Attachments").Create(&st).Error)
	require.NoError(t, conn.Create(&models.MediaAttachment{AccountId: 11, StatusId: &st.Id, FileName: "a.png", RemoteUrl: "https://8.8.8.8/a.png?sensitive=true", ContentType: "image/png", Type: models.MediaTypeImage}).Error)

	obj := map[string]any{"id": uri, "type": "Note", "content": "x", "sensitive": true, "attachment": []any{
		map[string]any{"type": "Document", "mediaType": "image/png", "url": "https://8.8.8.8/a.png?sensitive=true", "sensitive": true},
	}}
	prev := newStatusFlagsFetcher
	newStatusFlagsFetcher = func() activitypub.ActorFetcher {
		return &fakeActorFetcher{objects: map[string]map[string]any{uri: obj}}
	}
	t.Cleanup(func() { newStatusFlagsFetcher = prev })

	var before models.Status
	require.NoError(t, conn.First(&before, 422).Error)
	require.Nil(t, before.Domain)

	out, err := runStatuses("refresh-sensitive", "--rate", "0", "--dry-run")
	require.NoError(t, err, out)
	assert.Contains(t, out, "would be fixed: 1")
	assert.Contains(t, out, "newly sensitive 1")
	require.NoError(t, conn.First(&before, 422).Error)
	assert.Nil(t, before.Domain, "dry run writes nothing")
	assert.False(t, before.Sensitive)

	out, err = runStatuses("refresh-sensitive", "--rate", "0")
	require.NoError(t, err, out)
	var after models.Status
	require.NoError(t, conn.First(&after, 422).Error)
	require.NotNil(t, after.Domain)
	assert.Equal(t, "misskey.io", *after.Domain)
	assert.True(t, after.Sensitive)
	var att models.MediaAttachment
	require.NoError(t, conn.Where("status_id = ?", 422).First(&att).Error)
	assert.True(t, att.Sensitive)
}
