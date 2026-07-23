package activitypub

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupProxyTestDB(t *testing.T) *testutil.TestDB {
	return testutil.QuickDBSetup(t,
		&models.Account{},
		&models.AccountExtra{},
		&models.AccountKey{},
		&models.Follow{},
		&models.ProxyFollow{},
	)
}

func TestProxyFollowModel(t *testing.T) {
	testDB := setupProxyTestDB(t)

	t.Run("table name", func(t *testing.T) {
		pf := models.ProxyFollow{}
		assert.Equal(t, "proxy_follows", pf.TableName())
	})

	t.Run("CRUD", func(t *testing.T) {
		pf := &models.ProxyFollow{
			AccountID:        100,
			Reason:           models.ProxyFollowReasonList,
			ReasonID:         1,
			FollowActivityID: "https://example.com/activities/follow/1",
			Status:           models.ProxyFollowStatusPending,
		}
		err := testDB.GormDB.Create(pf).Error
		require.NoError(t, err)
		assert.NotZero(t, pf.ID)

		// Read back
		var fetched models.ProxyFollow

		err = testDB.GormDB.First(&fetched, pf.ID).Error
		require.NoError(t, err)
		assert.Equal(t, uint64(100), fetched.AccountID)
		assert.Equal(t, "list", fetched.Reason)
		assert.Equal(t, uint64(1), fetched.ReasonID)
		assert.Equal(t, models.ProxyFollowStatusPending, fetched.Status)

		// Update
		testDB.GormDB.Model(&fetched).Update("status", models.ProxyFollowStatusAccepted)

		var updated models.ProxyFollow

		testDB.GormDB.First(&updated, pf.ID)
		assert.Equal(t, models.ProxyFollowStatusAccepted, updated.Status)

		// Delete
		testDB.GormDB.Delete(&updated)

		var count int64

		testDB.GormDB.Model(&models.ProxyFollow{}).Where("id = ?", pf.ID).Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("unique constraint", func(t *testing.T) {
		pf1 := &models.ProxyFollow{
			AccountID: 200,
			Reason:    models.ProxyFollowReasonList,
			ReasonID:  10,
		}
		err := testDB.GormDB.Create(pf1).Error
		require.NoError(t, err)

		// Same (account_id, reason, reason_id) should fail
		pf2 := &models.ProxyFollow{
			AccountID: 200,
			Reason:    models.ProxyFollowReasonList,
			ReasonID:  10,
		}
		err = testDB.GormDB.Create(pf2).Error
		require.Error(t, err)

		// Different reason_id should succeed
		pf3 := &models.ProxyFollow{
			AccountID: 200,
			Reason:    models.ProxyFollowReasonList,
			ReasonID:  20,
		}
		err = testDB.GormDB.Create(pf3).Error
		require.NoError(t, err)
	})
}

func TestAccountKeyModel(t *testing.T) {
	testDB := setupProxyTestDB(t)

	t.Run("table name", func(t *testing.T) {
		ak := models.AccountKey{}
		assert.Equal(t, "account_keys", ak.TableName())
	})

	t.Run("CRUD", func(t *testing.T) {
		ak := &models.AccountKey{ //nolint:gosec // G101: reviewed and safe
			AccountID:  1,
			PublicKey:  "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----", //nolint:goconst
			PrivateKey: "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----",
		}
		err := testDB.GormDB.Create(ak).Error
		require.NoError(t, err)
		assert.NotZero(t, ak.ID)

		var fetched models.AccountKey

		err = testDB.GormDB.Where("account_id = ?", 1).First(&fetched).Error
		require.NoError(t, err)
		assert.Equal(t, uint64(1), fetched.AccountID)
		assert.Contains(t, fetched.PublicKey, "PUBLIC KEY")
		assert.Contains(t, fetched.PrivateKey, "PRIVATE KEY")
	})

	t.Run("unique account_id", func(t *testing.T) {
		ak1 := &models.AccountKey{
			AccountID:  999,
			PublicKey:  "pub1",
			PrivateKey: "priv1",
		}
		err := testDB.GormDB.Create(ak1).Error
		require.NoError(t, err)

		ak2 := &models.AccountKey{
			AccountID:  999,
			PublicKey:  "pub2",
			PrivateKey: "priv2",
		}
		err = testDB.GormDB.Create(ak2).Error
		require.Error(t, err)
	})
}

func TestProxyFollowConstants(t *testing.T) {
	assert.Equal(t, 0, models.ProxyFollowStatusPending)
	assert.Equal(t, 1, models.ProxyFollowStatusAccepted)
	assert.Equal(t, 2, models.ProxyFollowStatusRejected)
	assert.Equal(t, "list", models.ProxyFollowReasonList)
}

func TestProxyServiceNewProxyService(t *testing.T) {
	service := NewProxyService()
	assert.NotNil(t, service)
}

func TestProxyServiceHasLocalFollowers(t *testing.T) {
	testDB := setupProxyTestDB(t)

	// Create a remote account
	remoteDomain := "remote.com"
	remoteAccount := &models.Account{
		Username: "alice", //nolint:goconst
		Domain:   &remoteDomain,
		OriginId: "https://remote.com/users/alice",
	}
	require.NoError(t, testDB.GormDB.Create(remoteAccount).Error)

	// Create a local account
	localAccount := &models.Account{
		Username: "bob",
	}
	require.NoError(t, testDB.GormDB.Create(localAccount).Error)

	// Create proxy account
	proxyAccount := &models.Account{
		Username: proxyUsername,
		Bot:      true,
	}
	require.NoError(t, testDB.GormDB.Create(proxyAccount).Error)

	service := &ProxyService{db: testDB.GormDB}

	t.Run("no followers", func(t *testing.T) {
		assert.False(t, service.HasLocalFollowers(remoteAccount.Id))
	})

	t.Run("proxy follow does not count", func(t *testing.T) {
		proxyFollow := &models.Follow{
			AccountId: proxyAccount.Id,
			FollowId:  remoteAccount.Id,
			Status:    models.FollowStatusFollowed,
		}
		require.NoError(t, testDB.GormDB.Create(proxyFollow).Error)
		assert.False(t, service.HasLocalFollowers(remoteAccount.Id))
	})

	t.Run("real local follower counts", func(t *testing.T) {
		realFollow := &models.Follow{
			AccountId: localAccount.Id,
			FollowId:  remoteAccount.Id,
			Status:    models.FollowStatusFollowed,
		}
		require.NoError(t, testDB.GormDB.Create(realFollow).Error)
		assert.True(t, service.HasLocalFollowers(remoteAccount.Id))
	})

	t.Run("pending follow does not count", func(t *testing.T) {
		// Clean up real follow
		testDB.GormDB.Where("account_id = ?", localAccount.Id).Delete(&models.Follow{})

		pendingFollow := &models.Follow{
			AccountId: localAccount.Id,
			FollowId:  remoteAccount.Id,
			Status:    models.FollowStatusRequested,
		}
		require.NoError(t, testDB.GormDB.Create(pendingFollow).Error)
		assert.False(t, service.HasLocalFollowers(remoteAccount.Id))
	})
}

func TestProxyServiceHandleAccept(t *testing.T) {
	testDB := setupProxyTestDB(t)

	// Create proxy account + key (so GetOrCreateProxyAccount finds it)
	proxyAccount := &models.Account{
		Username: proxyUsername,
		Bot:      true,
	}
	require.NoError(t, testDB.GormDB.Create(proxyAccount).Error)

	proxyKey := &models.AccountKey{
		AccountID:  proxyAccount.Id,
		PublicKey:  "test-pub",
		PrivateKey: "test-priv",
	}
	require.NoError(t, testDB.GormDB.Create(proxyKey).Error)

	// Create remote account
	remoteDomain := "remote.com"
	remoteAccount := &models.Account{
		Username: "alice",
		Domain:   &remoteDomain,
		OriginId: "https://remote.com/users/alice",
	}
	require.NoError(t, testDB.GormDB.Create(remoteAccount).Error)

	service := &ProxyService{db: testDB.GormDB}

	t.Run("accept updates proxy follow and follow", func(t *testing.T) {
		// Create pending proxy follow
		pf := &models.ProxyFollow{
			AccountID:        remoteAccount.Id,
			Reason:           models.ProxyFollowReasonList,
			ReasonID:         1,
			FollowActivityID: "https://example.com/follow/1",
			Status:           models.ProxyFollowStatusPending,
		}
		require.NoError(t, testDB.GormDB.Create(pf).Error)

		// Create pending follow
		follow := &models.Follow{
			AccountId: proxyAccount.Id,
			FollowId:  remoteAccount.Id,
			Status:    models.FollowStatusRequested,
		}
		require.NoError(t, testDB.GormDB.Create(follow).Error)

		// Accept
		err := service.HandleAccept("https://example.com/follow/1")
		require.NoError(t, err)

		// Check proxy follow updated
		var updatedPF models.ProxyFollow

		testDB.GormDB.First(&updatedPF, pf.ID)
		assert.Equal(t, models.ProxyFollowStatusAccepted, updatedPF.Status)

		// Check follow updated
		var updatedFollow models.Follow

		testDB.GormDB.Where("account_id = ? AND follow_id = ?",
			proxyAccount.Id, remoteAccount.Id).First(&updatedFollow)
		assert.Equal(t, models.FollowStatusFollowed, updatedFollow.Status)
	})

	t.Run("accept non-existent activity returns error", func(t *testing.T) {
		err := service.HandleAccept("https://example.com/follow/nonexistent")
		require.Error(t, err)
	})
}

func TestProxyServiceGetProxyActorURI(t *testing.T) {
	testDB := testutil.QuickDBSetup(t,
		&models.Account{},
		&models.AccountExtra{},
		&models.AccountKey{},
		&models.Follow{},
		&models.ProxyFollow{},
		&models.SystemSetting{},
	)
	// systemsettings.Load() calls db.GetDB() — set global DB for this test
	database.SetTestDB(testDB.GormDB)
	service := &ProxyService{db: testDB.GormDB}
	uri := service.getProxyActorURI()
	// With no domain setting configured, defaults to "localhost"
	assert.Equal(t, "https://localhost/users/system.proxy", uri)
}

func TestParsePrivateKey(t *testing.T) {
	t.Run("invalid PEM returns error", func(t *testing.T) {
		_, err := parsePrivateKey("not a PEM")
		require.Error(t, err)
	})

	t.Run("valid PEM round-trip", func(t *testing.T) {
		// Generate a real RSA key
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)

		privPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key),
		})

		parsed, err := parsePrivateKey(string(privPEM))
		require.NoError(t, err)
		assert.NotNil(t, parsed)
		assert.Equal(t, key.D, parsed.D)
	})
}

func TestProxyServiceCleanupProxyFollow(t *testing.T) {
	testDB := setupProxyTestDB(t)

	// Create proxy account + key
	proxyAccount := &models.Account{
		Username: proxyUsername,
		Bot:      true,
	}
	require.NoError(t, testDB.GormDB.Create(proxyAccount).Error)

	proxyKey := &models.AccountKey{
		AccountID:  proxyAccount.Id,
		PublicKey:  "test-pub",
		PrivateKey: "test-priv",
	}
	require.NoError(t, testDB.GormDB.Create(proxyKey).Error)

	// Create remote account
	remoteDomain := "remote.com"
	remoteAccount := &models.Account{
		Username: "alice",
		Domain:   &remoteDomain,
		OriginId: "https://remote.com/users/alice",
	}
	require.NoError(t, testDB.GormDB.Create(remoteAccount).Error)

	service := &ProxyService{db: testDB.GormDB}

	t.Run("cleanup with remaining references keeps follow", func(t *testing.T) {
		// Two lists reference alice
		pf1 := &models.ProxyFollow{
			AccountID: remoteAccount.Id,
			Reason:    models.ProxyFollowReasonList,
			ReasonID:  1,
			Status:    models.ProxyFollowStatusAccepted,
		}
		pf2 := &models.ProxyFollow{
			AccountID: remoteAccount.Id,
			Reason:    models.ProxyFollowReasonList,
			ReasonID:  2,
			Status:    models.ProxyFollowStatusAccepted,
		}

		require.NoError(t, testDB.GormDB.Create(pf1).Error)
		require.NoError(t, testDB.GormDB.Create(pf2).Error)

		// Proxy follows alice
		follow := &models.Follow{
			AccountId: proxyAccount.Id,
			FollowId:  remoteAccount.Id,
			Status:    models.FollowStatusFollowed,
		}
		require.NoError(t, testDB.GormDB.Create(follow).Error)

		// Remove from list 1
		err := service.CleanupProxyFollow(remoteAccount.Id, models.ProxyFollowReasonList, 1)
		require.NoError(t, err)

		// pf1 should be deleted, pf2 should remain
		var remaining int64

		testDB.GormDB.Model(&models.ProxyFollow{}).Where("account_id = ?", remoteAccount.Id).Count(&remaining)
		assert.Equal(t, int64(1), remaining)

		// Follow should still exist
		var followCount int64

		testDB.GormDB.Model(&models.Follow{}).Where("account_id = ? AND follow_id = ?",
			proxyAccount.Id, remoteAccount.Id).Count(&followCount)
		assert.Equal(t, int64(1), followCount)
	})

	t.Run("cleanup last reference deletes follow", func(t *testing.T) {
		// Remove from list 2 (last reference)
		// Note: sendUndoFollowActivity will fail (no real remote server)
		// but the local follow record should still be deleted
		err := service.CleanupProxyFollow(remoteAccount.Id, models.ProxyFollowReasonList, 2)
		require.NoError(t, err)

		// No proxy follows should remain
		var remaining int64

		testDB.GormDB.Model(&models.ProxyFollow{}).Where("account_id = ?", remoteAccount.Id).Count(&remaining)
		assert.Equal(t, int64(0), remaining)

		// Follow should be deleted
		var followCount int64

		testDB.GormDB.Model(&models.Follow{}).Where("account_id = ? AND follow_id = ?",
			proxyAccount.Id, remoteAccount.Id).Count(&followCount)
		assert.Equal(t, int64(0), followCount)
	})
}

func TestProxyServiceEnsureProxyFollowLocalAccount(t *testing.T) {
	testDB := setupProxyTestDB(t)

	// Create proxy account + key
	proxyAccount := &models.Account{
		Username: proxyUsername,
		Bot:      true,
	}
	require.NoError(t, testDB.GormDB.Create(proxyAccount).Error)
	proxyKey := &models.AccountKey{
		AccountID:  proxyAccount.Id,
		PublicKey:  "test-pub",
		PrivateKey: "test-priv",
	}
	require.NoError(t, testDB.GormDB.Create(proxyKey).Error)

	// Create local account (not remote)
	localAccount := &models.Account{
		Username: "local-user",
	}
	require.NoError(t, testDB.GormDB.Create(localAccount).Error)

	service := &ProxyService{db: testDB.GormDB}

	// Should skip silently for local accounts
	err := service.EnsureProxyFollow(localAccount.Id, models.ProxyFollowReasonList, 1)
	require.NoError(t, err)

	// No proxy follow should be created
	var count int64

	testDB.GormDB.Model(&models.ProxyFollow{}).Count(&count)
	assert.Equal(t, int64(0), count)
}

func TestProxyServiceEnsureProxyFollowIdempotent(t *testing.T) {
	testDB := setupProxyTestDB(t)

	// Create proxy account + key
	proxyAccount := &models.Account{
		Username: proxyUsername,
		Bot:      true,
	}
	require.NoError(t, testDB.GormDB.Create(proxyAccount).Error)
	proxyKey := &models.AccountKey{
		AccountID:  proxyAccount.Id,
		PublicKey:  "test-pub",
		PrivateKey: "test-priv",
	}
	require.NoError(t, testDB.GormDB.Create(proxyKey).Error)

	// Create remote account
	remoteDomain := "remote.com"
	remoteAccount := &models.Account{
		Username: "alice",
		Domain:   &remoteDomain,
		OriginId: "https://remote.com/users/alice",
	}
	require.NoError(t, testDB.GormDB.Create(remoteAccount).Error)

	// Create a real local follower so we don't try to send HTTP
	localAccount := &models.Account{Username: "bob"}
	require.NoError(t, testDB.GormDB.Create(localAccount).Error)
	realFollow := &models.Follow{
		AccountId: localAccount.Id,
		FollowId:  remoteAccount.Id,
		Status:    models.FollowStatusFollowed,
	}
	require.NoError(t, testDB.GormDB.Create(realFollow).Error)

	service := &ProxyService{db: testDB.GormDB}

	// First call should create proxy follow
	err := service.EnsureProxyFollow(remoteAccount.Id, models.ProxyFollowReasonList, 1)
	require.NoError(t, err)

	var count int64

	testDB.GormDB.Model(&models.ProxyFollow{}).Count(&count)
	assert.Equal(t, int64(1), count)

	// Second call should be idempotent
	err = service.EnsureProxyFollow(remoteAccount.Id, models.ProxyFollowReasonList, 1)
	require.NoError(t, err)

	testDB.GormDB.Model(&models.ProxyFollow{}).Count(&count)
	assert.Equal(t, int64(1), count) // Still 1
}
