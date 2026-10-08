package activitypub

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/kasuganosora/community/db/database"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/tests/pgtest"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestStatusObjectVisibilityAndOwner(t *testing.T) {
	conn, err := pgtest.GormOpen(t, sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := conn.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	previous := database.GetDB()
	database.SetTestDB(conn)
	t.Cleanup(func() { database.SetTestDB(previous) })
	require.NoError(t, conn.AutoMigrate(&models.Account{}, &models.AccountExtra{}, &models.Status{}, &models.StatusContent{}, &models.StatusToAccount{}, &models.Tag{}, &models.StatusTag{}, &models.MediaAttachment{}, &models.Follow{}, &models.Block{}))
	raw := conn.Session(&gorm.Session{SkipHooks: true})
	author := &models.Account{Id: 910, Username: "object_author"}
	require.NoError(t, raw.Omit("User").Create(author).Error)
	domain := "remote.example"
	viewer := &models.Account{Id: 911, Username: "follower", Domain: &domain, OriginId: "https://remote.example/users/follower"}
	outsider := &models.Account{Id: 912, Username: "outsider", Domain: &domain, OriginId: "https://remote.example/users/outsider"}
	require.NoError(t, raw.Omit("User").Create(viewer).Error)
	require.NoError(t, raw.Omit("User").Create(outsider).Error)
	require.NoError(t, raw.Create(&models.Follow{AccountId: viewer.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	require.NoError(t, raw.Create(&models.Follow{AccountId: outsider.Id, FollowId: author.Id, Status: models.FollowStatusRequested}).Error)
	statuses := map[string]*models.Status{}
	for i, item := range []struct {
		name       string
		visibility uint8
	}{{"public", models.StatusVisibilityPublic}, {"unlisted", models.StatusVisibilityUnlisted}, {"private", models.StatusVisibilityPrivate}, {"direct", models.StatusVisibilityDirect}, {"scheduled", models.StatusVisibilityPublic}, {"deleted", models.StatusVisibilityPublic}} {
		now := time.Now()
		s := &models.Status{Id: uint64(920 + i), AccountId: author.Id, Visibility: item.visibility, CreatedAt: &now, UpdatedAt: &now, Content: models.StatusContent{Content: "confidential fixture"}}
		if item.name == "scheduled" {
			s.ScheduledAt = &now
		}
		if item.name == "deleted" {
			s.DeletedAt = &now
		}
		require.NoError(t, raw.Create(s).Error)
		statuses[item.name] = s
	}
	require.NoError(t, raw.Create(&models.StatusToAccount{StatusId: statuses["direct"].Id, AccountId: viewer.Id}).Error)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	for _, a := range []*models.Account{viewer, outsider} {
		globalKeyCache.Add(a.OriginId+"#main-key", &key.PublicKey)
		t.Cleanup(func() { globalKeyCache.Remove(a.OriginId + "#main-key") })
	}
	router := gin.New()
	router.GET("/users/:username/statuses/:id", StatusObject)
	for _, tc := range []struct {
		name, status, user string
		signer             *models.Account
		want               int
	}{
		{"public anonymous", "public", author.Username, nil, 200}, {"unlisted anonymous", "unlisted", author.Username, nil, 200},
		{"private unsigned", "private", author.Username, nil, 401}, {"direct unsigned", "direct", author.Username, nil, 401},
		{"accepted follower", "private", author.Username, viewer, 200},
		{"forged follower signature", "private", author.Username, viewer, 401}, {"pending follower denied", "private", author.Username, outsider, 403},
		{"direct mention", "direct", author.Username, viewer, 200}, {"direct outsider", "direct", author.Username, outsider, 403},
		{"wrong owner", "public", "different_user", nil, 404}, {"scheduled", "scheduled", author.Username, nil, 404}, {"deleted", "deleted", author.Username, nil, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "https://local.example/users/"+tc.user+"/statuses/"+strconv.FormatUint(statuses[tc.status].Id, 10), nil)
			if tc.signer != nil {
				req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
				signing, err := BuildSigningString(req, []string{"(request-target)", "host", "date"})
				require.NoError(t, err)
				signed, err := SignWithRSA(key, []byte(signing))
				require.NoError(t, err)
				req.Header.Set("Signature", fmt.Sprintf(`keyId="%s#main-key",algorithm="rsa-sha256",headers="(request-target) host date",signature="%s"`, tc.signer.OriginId, base64.StdEncoding.EncodeToString(signed)))
			}
			if tc.name == "forged follower signature" {
				req.Header.Set("Date", time.Now().Add(-24*time.Hour).UTC().Format(http.TimeFormat))
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tc.want, w.Code, w.Body.String())
			if tc.want == 200 {
				require.Equal(t, "application/activity+json", w.Header().Get("Content-Type"))
				var object map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &object))
				require.Equal(t, "Note", object["type"])
			} else {
				require.NotContains(t, w.Body.String(), "confidential fixture")
			}
		})
	}
}

func TestStatusNoteRemoteRecipientsAndReplyURI(t *testing.T) {
	// Audience mapping is pure; remote actors must never become local /users URLs.
	domain := "misskey.example"
	author := &models.Account{Username: "alice"}
	remote := &models.Account{Username: "bob", Domain: &domain, OriginId: "https://misskey.example/users/opaque-id"}
	for _, visibility := range []uint8{models.StatusVisibilityPublic, models.StatusVisibilityUnlisted, models.StatusVisibilityPrivate, models.StatusVisibilityDirect} {
		status := &models.Status{Visibility: visibility, ToAccounts: models.Accounts{remote}, ReplyAccount: remote}
		to, cc := statusRecipients(status, author, "https://local.example")
		require.Contains(t, append(append([]string{}, to...), cc...), remote.OriginId)
		if visibility >= models.StatusVisibilityPrivate {
			require.NotContains(t, to, publicAddress)
			require.NotContains(t, cc, publicAddress)
		}
		if visibility == models.StatusVisibilityDirect {
			require.Equal(t, []string{remote.OriginId}, to)
			require.Empty(t, cc)
		}
	}
	origin := "https://misskey.example/notes/opaque-note"
	require.Equal(t, origin, statusObjectURI(&models.Status{OriginUri: &origin}, remote, "https://local.example"))
}
