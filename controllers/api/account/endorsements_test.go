package account

import (
	"encoding/json"
	"testing"

	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListEndorsements(t *testing.T) {
	db := featuredTagsDB(t)
	require.NoError(t, db.AutoMigrate(&models.AccountEmoji{}, &models.Emoji{}, &Endorsement{}))
	alice := &models.Account{Id: 1, Username: "alice"}
	for _, acc := range []*models.Account{alice, {Id: 2, Username: "bob"}, {Id: 3, Username: "carol"}} {
		require.NoError(t, db.Create(acc).Error)
	}
	require.NoError(t, db.Create(&Endorsement{AccountId: 1, TargetId: 2}).Error)
	require.NoError(t, db.Create(&Endorsement{AccountId: 1, TargetId: 3}).Error)
	require.NoError(t, db.Create(&Endorsement{AccountId: 2, TargetId: 3}).Error)
	assert.Error(t, db.Create(&Endorsement{AccountId: 1, TargetId: 2}).Error, "unique per account and target")

	res := callFeatured(t, ListEndorsements, alice, "GET", nil, nil)
	require.Equal(t, 200, res.code, string(res.body))
	var accounts []map[string]any
	require.NoError(t, json.Unmarshal(res.body, &accounts))
	require.Len(t, accounts, 2)
	assert.Equal(t, "carol", accounts[0]["username"], "newest endorsement first")
	assert.Equal(t, "bob", accounts[1]["username"])

	require.NoError(t, db.Migrator().DropTable(&Endorsement{}))
	res = callFeatured(t, ListEndorsements, alice, "GET", nil, nil)
	assert.Equal(t, 500, res.code, "database errors are not an empty list")
}
