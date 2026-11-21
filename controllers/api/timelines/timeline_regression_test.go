package timelines

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestList_PrivateListRejectsAnonymousAndStrangerWithoutNilPanic(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	owner := makeAccount(1)
	stranger := makeAccount(2)
	require.NoError(t, testDB.Create(owner).Error)
	require.NoError(t, testDB.Create(stranger).Error)

	privateList := &models.List{
		Id:         42,
		Title:      "Secret List",
		AccountId:  owner.Id,
		Visibility: models.ListVisibilityPrivate,
	}
	require.NoError(t, testDB.Create(privateList).Error)

	// 1. Anonymous visitor: must get 403 without nil-pointer panic or updating last_viewed_at.
	cAnon, wAnon := newTestContext(t, "GET", "/api/v1/timelines/list/42", "limit=10")
	cAnon.Params = gin.Params{{Key: "id", Value: "42"}}
	safeHandler(t, func() {
		List(cAnon)
	})
	assert.Equal(t, http.StatusForbidden, wAnon.Code)

	// 2. Non-follower logged-in stranger: must get 403 without updating last_viewed_at.
	cStranger, wStranger := newTestContext(t, "GET", "/api/v1/timelines/list/42", "limit=10")
	setAccount(cStranger, stranger)
	cStranger.Params = gin.Params{{Key: "id", Value: "42"}}
	safeHandler(t, func() {
		List(cStranger)
	})
	assert.Equal(t, http.StatusForbidden, wStranger.Code)

	var reloaded models.List
	require.NoError(t, testDB.First(&reloaded, privateList.Id).Error)
	assert.Nil(t, reloaded.LastViewedAt, "unauthorized list access must not mutate last_viewed_at")
}

func TestNotificationTimeline_ExclusiveSinceIdUnknownTypesAndForwardMinId(t *testing.T) {
	testDB := setupTestDB(t)
	setGlobalTestDB(t, testDB)

	now := time.Now()
	viewer := makeAccount(1)
	sender := makeAccount(2)
	require.NoError(t, testDB.Create(viewer).Error)
	require.NoError(t, testDB.Create(sender).Error)

	for _, id := range []uint64{10, 20, 30, 40} {
		require.NoError(t, testDB.Create(&models.Notification{
			Id:            id,
			Type:          models.NotificationTypeFollow,
			AccountId:     viewer.Id,
			FromAccountId: sender.Id,
			CreatedAt:     &now,
			UpdatedAt:     &now,
		}).Error)
	}

	// 1. Unknown types[] must return [] rather than ignoring the filter.
	cUnknown, wUnknown := newTestContext(t, "GET", "/api/v1/notifications", "types[]=totally_unknown_type")
	setAccount(cUnknown, viewer)
	safeHandler(t, func() { NotificationTimeline(cUnknown) })
	require.Equal(t, http.StatusOK, wUnknown.Code)
	var unknownItems []map[string]any
	require.NoError(t, json.Unmarshal(wUnknown.Body.Bytes(), &unknownItems))
	assert.Empty(t, unknownItems)

	// 2. since_id=40 must be exclusive (> 40), returning empty instead of repeating 40.
	cSince, wSince := newTestContext(t, "GET", "/api/v1/notifications", "since_id=40")
	setAccount(cSince, viewer)
	safeHandler(t, func() { NotificationTimeline(cSince) })
	require.Equal(t, http.StatusOK, wSince.Code)
	var sinceItems []map[string]any
	require.NoError(t, json.Unmarshal(wSince.Body.Bytes(), &sinceItems))
	assert.Empty(t, sinceItems)

	// 3. min_id=10&limit=2 must return immediately-newer [30, 20], not [40, 30].
	cMin, wMin := newTestContext(t, "GET", "/api/v1/notifications", "min_id=10&limit=2")
	setAccount(cMin, viewer)
	safeHandler(t, func() { NotificationTimeline(cMin) })
	require.Equal(t, http.StatusOK, wMin.Code)
	var minItems []struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(wMin.Body.Bytes(), &minItems))
	require.Len(t, minItems, 2)
	assert.Equal(t, "30", minItems[0].ID)
	assert.Equal(t, "20", minItems[1].ID)
}
