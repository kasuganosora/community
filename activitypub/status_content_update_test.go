package activitypub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestHandleUpdateActivityUsesParentStatusIDWhenContentRowMissing(t *testing.T) {
	conn := persistenceDB(t, false)
	require.NoError(t, conn.AutoMigrate(&models.StatusHistory{}, &models.MediaAttachment{}, &models.Emoji{}))

	now := time.Now()
	domain := "remote.example"
	actorURI := "https://remote.example/users/alice"
	remoteAuthor := models.Account{
		Id:       50,
		Username: "alice",
		Acct:     "alice@remote.example",
		Domain:   &domain,
		OriginId: actorURI,
	}
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Extra", "User").Create(&remoteAuthor).Error)

	for _, smallID := range []uint64{1, 2} {
		u := "https://remote.example/notes/seed-" + string(rune('0'+smallID))
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Content").Create(&models.Status{
			Id: smallID, AccountId: remoteAuthor.Id, Domain: &domain, OriginUri: &u,
			Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now,
		}).Error)
		require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Create(&models.StatusContent{
			StatusId: smallID, ContentFormat: "plain", Content: "occupied-small-row",
		}).Error)
	}

	targetURI := "https://remote.example/notes/10500"
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Content").Create(&models.Status{
		Id: 10500, AccountId: remoteAuthor.Id, Domain: &domain, OriginUri: &targetURI,
		Visibility: models.StatusVisibilityPublic, CreatedAt: &now, UpdatedAt: &now,
	}).Error)

	activity := map[string]any{
		"id":    "https://remote.example/activities/update-10500",
		"type":  "Update",
		"actor": actorURI,
		"object": map[string]any{
			"id":           targetURI,
			"type":         "Note",
			"attributedTo": actorURI,
			"content":      "<p>Updated caption</p>",
			"summary":      "cw",
			"to":           []any{"https://www.w3.org/ns/activitystreams#Public"},
		},
	}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	raw, _ := json.Marshal(activity)
	c.Request = httptest.NewRequest(http.MethodPost, "/inbox", bytes.NewReader(raw))

	ctx := context.WithValue(context.Background(), actorKey, actorURI)
	handleUpdateActivity(ctx, c, activity, nil)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var repaired models.StatusContent
	require.NoError(t, conn.First(&repaired, 10500).Error)
	require.Equal(t, uint64(10500), repaired.StatusId)
	require.Contains(t, repaired.Content, "Updated caption")
	require.Equal(t, "cw", repaired.SpoilerText)

	for _, smallID := range []uint64{1, 2} {
		var small models.StatusContent
		require.NoError(t, conn.First(&small, smallID).Error)
		require.Equal(t, "occupied-small-row", small.Content)
	}
	var total int64
	require.NoError(t, conn.Model(&models.StatusContent{}).Count(&total).Error)
	require.EqualValues(t, 3, total)
}
