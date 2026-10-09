package commands

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func runRelays(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	app := &cli.App{Writer: &out, Commands: []*cli.Command{RelaysCommand()}}
	err := app.Run(append([]string{"tootctl", "relays"}, args...))
	return out.String(), err
}

func TestRelaysCommand(t *testing.T) {
	conn := testutil.QuickDBSetup(t, &models.Relay{}, &activitypub.Delivery{}, &models.SystemSetting{},
		&models.Account{}, &models.AccountKey{}, &models.AccountExtra{}, &models.AccountFederationKey{}).GormDB
	database.SetTestDB(conn)
	t.Cleanup(database.ResetTestDB)
	prevURL, prevConfigured := config.C.AppBaseURL, config.AppBaseURLConfigured()
	config.SetAppBaseURL("https://relaycli.example", true)
	t.Cleanup(func() { config.SetAppBaseURL(prevURL, prevConfigured) })

	out, err := runRelays(t, "add", "https://relay.one.example/inbox", "https://relay.two.example/inbox")
	require.NoError(t, err, out)
	require.Contains(t, out, "https://relay.one.example/inbox: added (id ")
	require.Contains(t, out, "Follow queued from https://relaycli.example/actor")

	_, err = runRelays(t, "add", "not a url")
	require.Error(t, err)
	_, err = runRelays(t, "add", "https://relay.one.example/inbox")
	require.Error(t, err, "duplicates are refused")

	var deliveries []activitypub.Delivery
	require.NoError(t, conn.Order("id").Find(&deliveries).Error)
	require.Len(t, deliveries, 2)
	var follow map[string]any
	require.NoError(t, json.Unmarshal([]byte(deliveries[0].Payload), &follow))
	require.Equal(t, "Follow", follow["type"])
	require.Equal(t, "https://relaycli.example/actor", follow["actor"])
	require.Equal(t, "https://www.w3.org/ns/activitystreams#Public", follow["object"])
	require.NotContains(t, follow, "to")

	// The instance actor exists with its key.
	var actor models.Account
	require.NoError(t, conn.Where("local_actor_uri = ?", "https://relaycli.example/actor").First(&actor).Error)
	var keys int64
	require.NoError(t, conn.Model(&models.AccountKey{}).Where("account_id = ?", actor.Id).Count(&keys).Error)
	require.EqualValues(t, 1, keys)

	out, err = runRelays(t, "list")
	require.NoError(t, err)
	require.Contains(t, out, "pending")
	require.Contains(t, out, "https://relay.two.example/inbox")

	_, err = runRelays(t, "disable", "https://relay.one.example/inbox")
	require.NoError(t, err)
	out, err = runRelays(t, "list")
	require.NoError(t, err)
	require.Contains(t, out, "idle")
	_, err = runRelays(t, "enable", "https://relay.one.example/inbox")
	require.NoError(t, err)
	_, err = runRelays(t, "remove", "https://relay.two.example/inbox")
	require.NoError(t, err)
	out, err = runRelays(t, "list")
	require.NoError(t, err)
	require.NotContains(t, out, "relay.two.example")
	// A removed relay can be added again.
	_, err = runRelays(t, "add", "https://relay.two.example/inbox")
	require.NoError(t, err)
	_, err = runRelays(t, "enable", "999")
	require.Error(t, err)
}
