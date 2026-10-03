package models

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"strings"
	"testing"
)

func TestImportedPasswordRequiresExplicitScheme(t *testing.T) {
	password := strings.Repeat("a", 72) + "source suffix"
	hash, err := bcrypt.GenerateFromPassword([]byte(password[:72]), bcrypt.MinCost)
	require.NoError(t, err)
	secret := UserSecretData{Password: string(hash), Salt: "community encryption salt", PasswordScheme: "bcrypt"}
	require.NoError(t, secret.CheckPassword(password))
	require.Error(t, secret.CheckPassword("wrong"))
	secret.PasswordScheme = ""
	require.Error(t, secret.CheckPassword(password))
	native, err := EncryptPassword(password, secret.Salt)
	require.NoError(t, err)
	secret.Password = native
	require.NoError(t, secret.CheckPassword(password))
	secret.PasswordScheme = "unknown"
	require.Error(t, secret.CheckPassword(password))
}
func TestImportedObjectIdentitySurvivesLocalSemantics(t *testing.T) {
	account := &Account{Id: 7, Username: "alice", LocalActorURI: "https://example.test/ap/users/123"}
	status := &Status{Id: 8, Account: account, LocalObjectURI: "https://example.test/notes/opaque"}
	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	var snapshot Status
	require.NoError(t, json.Unmarshal(encoded, &snapshot))
	require.Equal(t, account.LocalActorURI, snapshot.Account.ActorURI("https://example.test"))
	require.Equal(t, status.LocalObjectURI, snapshot.ObjectURI("https://example.test"))
	require.True(t, status.IsLocal())
	require.Equal(t, "https://example.test/ap/users/123", account.ActorURI("https://example.test"))
	require.Equal(t, "https://example.test/notes/opaque", status.ObjectURI("https://example.test"))
}
