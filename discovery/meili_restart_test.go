package discovery

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMeiliRealEngineRebuildAdvancesCutoffAfterRestart(t *testing.T) {
	host := strings.TrimRight(os.Getenv("DISCOVERY_TEST_MEILI_URL"), "/")
	if host == "" {
		t.Skip("set DISCOVERY_TEST_MEILI_URL to an isolated real Meilisearch")
	}
	p, settings := meiliTestProvider(t, host)
	settings.RequireManualApproval = false
	conn := meiliSQLFixture(t)
	p.conn = func() *gorm.DB { return conn }
	now := time.Now().UTC().Add(-time.Minute)
	write := conn.Session(&gorm.Session{SkipHooks: true})
	domain, allow := "restart.example", true
	require.NoError(t, write.Omit("Extra", "User").Create(&models.Account{Id: 2, Username: "remote", Acct: "remote@" + domain, Domain: &domain, Discoverable: true, RemoteDiscoverable: &allow, RemoteIndexable: &allow, RemoteDiscoveryCheckedAt: &now}).Error)
	require.NoError(t, write.Omit("Content").Create(&models.Status{Id: 5, AccountId: 2, Visibility: models.StatusVisibilityPublic, CreatedAt: &now, Language: "en"}).Error)
	require.NoError(t, write.Create(&models.StatusContent{StatusId: 5, Content: "art photography"}).Error)
	t.Cleanup(func() {
		state, _ := p.readState(context.Background())
		if state.Active != nil {
			_ = p.retire(context.Background(), state.Active.Index)
		}
	})
	require.NoError(t, p.rebuild(context.Background()))
	require.True(t, p.Status().Ready, p.Status().Reason)

	// Restart: a new process with the same Redis/Meili and an older cutoff.
	restarted, _ := meiliTestProvider(t, host)
	restarted.connection.Prefix = p.connection.Prefix
	restarted.redis = p.redis
	restarted.settings = p.settings
	restarted.conn = p.conn
	restarted.maxDatabaseBytes = p.maxDatabaseBytes
	state, err := restarted.readState(context.Background())
	require.NoError(t, err)
	before := state.Active.Cutoff
	state.Active.Cutoff = time.Now().UTC().Add(-3 * time.Minute)
	ctx, token, release, err := restarted.lease(context.Background(), time.Second)
	require.NoError(t, err)
	require.NoError(t, restarted.writeState(ctx, token, state))
	release()
	require.NoError(t, restarted.rebuild(context.Background()))
	after, err := restarted.readState(context.Background())
	require.NoError(t, err)
	require.True(t, after.Active.Cutoff.After(before), "cutoff must advance")
	require.True(t, restarted.Status().Ready, restarted.Status().Reason)
}
