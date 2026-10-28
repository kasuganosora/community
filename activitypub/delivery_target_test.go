package activitypub

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeliveryTargetFailureAddsFiveMinutes(t *testing.T) {
	conn := outboundDB(t)
	inbox := "https://dead.example/inbox"
	delivery := newCooldownDelivery(t, conn, "cooldown_sender", inbox)
	installCooldownTransport(t, func(r *http.Request) (*http.Response, error) {
		return outboundResponse(502, "bad gateway"), nil
	})

	NewDeliveryQueue(nil, "", 0).processPending()
	row := readDeliveryTarget(t, conn, inbox)
	require.Equal(t, int64((5*time.Minute)/time.Second), row.Cooldown, "one delivery failure adds 5 minutes")
	require.Nil(t, row.CooldownUntil, "5 minutes is within the 60 minute grace, so the window is not enforced")

	past := time.Now().Add(-time.Second)
	require.NoError(t, conn.Model(&delivery).Update("next_retry_at", past).Error)
	NewDeliveryQueue(nil, "", 0).processPending()
	row = readDeliveryTarget(t, conn, inbox)
	require.Equal(t, int64((10*time.Minute)/time.Second), row.Cooldown, "each later failure adds another 5 minutes")
	require.Nil(t, row.CooldownUntil)
}

func TestDeliveryTargetWithinGraceStillAttempts(t *testing.T) {
	conn := outboundDB(t)
	inbox := "https://flaky.example/inbox"
	now := time.Now()
	for range 12 {
		require.NoError(t, noteDeliveryTargetFailure(now, inbox))
	}
	row := readDeliveryTarget(t, conn, inbox)
	require.Equal(t, int64((60*time.Minute)/time.Second), row.Cooldown)
	require.Nil(t, row.CooldownUntil, "an accumulated cooldown of exactly 60 minutes is not enforced")

	delivery := newCooldownDelivery(t, conn, "grace_sender", inbox)
	aged := now.Add(-48 * time.Hour)
	require.NoError(t, conn.Model(&delivery).Update("created_at", aged).Error)
	posts := 0
	installCooldownTransport(t, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, inbox, r.URL.String())
		posts++
		return outboundResponse(202, ""), nil
	})
	NewDeliveryQueue(nil, "", 0).processPending()
	require.Equal(t, 1, posts, "grace is the accumulated cooldown, not the age of the activity")
	require.NoError(t, conn.First(&delivery, delivery.ID).Error)
	require.Equal(t, DeliveryStatusDelivered, delivery.Status)
}

func TestDeliveryTargetBeyondGraceSkipsUntilElapsed(t *testing.T) {
	conn := outboundDB(t)
	dead := "https://relay.example/inbox"
	healthyInbox := "https://healthy.example/inbox"
	now := time.Now()
	for range 13 {
		require.NoError(t, noteDeliveryTargetFailure(now, dead))
	}
	row := readDeliveryTarget(t, conn, dead)
	require.Equal(t, int64((65*time.Minute)/time.Second), row.Cooldown)
	require.NotNil(t, row.CooldownUntil)
	require.WithinDuration(t, now.Add(65*time.Minute), *row.CooldownUntil, time.Second)

	author := outboundAccount(t, conn, "relay_sender", false)
	actor := systemsettings.GetBaseURL() + "/users/" + author.Username
	payload, err := json.Marshal(map[string]any{"id": actor + "/activities/1", "actor": actor, "type": "Create", "object": "https://social.example/notes/1"})
	require.NoError(t, err)
	historical := Delivery{ActivityID: actor + "/activities/old", TargetInbox: dead, Payload: string(payload), Status: DeliveryStatusFailed, LastError: "historical"}
	require.NoError(t, conn.Create(&historical).Error)
	for range 12 {
		require.NoError(t, conn.Create(&Delivery{ActivityID: actor + "/activities/dead", TargetInbox: dead, Payload: string(payload), Status: DeliveryStatusPending}).Error)
	}
	healthy := Delivery{ActivityID: actor + "/activities/2", TargetInbox: healthyInbox, Payload: string(payload), Status: DeliveryStatusPending}
	require.NoError(t, conn.Create(&healthy).Error)

	posts := map[string]int{}
	installCooldownTransport(t, func(r *http.Request) (*http.Response, error) {
		posts[r.URL.String()]++
		if r.URL.String() == dead {
			return outboundResponse(502, "down"), nil
		}
		return outboundResponse(202, ""), nil
	})
	NewDeliveryQueue(nil, "", 0).processPending()
	require.Zero(t, posts[dead], "a target whose accumulated cooldown is over 60 minutes is skipped")
	require.Equal(t, 1, posts[healthyInbox], "skipped relay rows must not fill the batch ahead of a healthier inbox")
	require.NoError(t, conn.First(&historical, historical.ID).Error)
	require.Equal(t, DeliveryStatusFailed, historical.Status, "a cooldown must not redeliver historical failures")

	elapsed := time.Now().Add(-time.Second)
	require.NoError(t, conn.Model(&DeliveryTarget{}).Where("target = ?", dead).Update("cooldown_until", elapsed).Error)
	NewDeliveryQueue(nil, "", 0).processPending()
	require.Equal(t, 1, posts[dead], "the target is attempted again once the cooldown window has elapsed")
	row = readDeliveryTarget(t, conn, dead)
	require.Equal(t, int64((70*time.Minute)/time.Second), row.Cooldown, "the elapsed attempt's failure adds another 5 minutes")
	require.NotNil(t, row.CooldownUntil)
	require.True(t, row.CooldownUntil.After(time.Now().Add(69*time.Minute)))

	NewDeliveryQueue(nil, "", 0).processPending()
	require.Equal(t, 1, posts[dead], "the new window suppresses another attempt")
}

func TestDeliveryTargetSuccessClearsCooldown(t *testing.T) {
	conn := outboundDB(t)
	inbox := "https://recovered.example/inbox"
	other := "https://other.example/inbox"
	now := time.Now()
	for range 13 {
		require.NoError(t, noteDeliveryTargetFailure(now, inbox))
		require.NoError(t, noteDeliveryTargetFailure(now, other))
	}
	elapsed := now.Add(-time.Second)
	require.NoError(t, conn.Model(&DeliveryTarget{}).Where("target = ?", inbox).Update("cooldown_until", elapsed).Error)

	author := outboundAccount(t, conn, "clear_sender", false)
	remote := outboundAccount(t, conn, "clear_peer", true)
	require.NoError(t, noteDeliveryTargetFailure(now, remote.OriginId))
	actor := systemsettings.GetBaseURL() + "/users/" + author.Username
	payload, err := json.Marshal(map[string]any{"id": actor + "/activities/1", "actor": actor, "type": "Create", "object": "https://social.example/notes/9"})
	require.NoError(t, err)
	auth, err := json.Marshal(DeliveryAuthorization{AccountID: author.Id, RecipientIDs: []uint64{remote.Id}})
	require.NoError(t, err)
	delivery := Delivery{ActivityID: actor + "/activities/1", TargetInbox: inbox, Payload: string(payload), Authorization: string(auth), Status: DeliveryStatusPending}
	require.NoError(t, conn.Create(&delivery).Error)

	installCooldownTransport(t, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, inbox, r.URL.String())
		return outboundResponse(202, ""), nil
	})
	NewDeliveryQueue(nil, "", 0).processPending()
	require.NoError(t, conn.First(&delivery, delivery.ID).Error)
	require.Equal(t, DeliveryStatusDelivered, delivery.Status)

	cleared := readDeliveryTarget(t, conn, inbox)
	require.Zero(t, cleared.Cooldown)
	require.Nil(t, cleared.CooldownUntil, "a successful delivery clears the accumulated cooldown immediately")
	actorRow := readDeliveryTarget(t, conn, remote.OriginId)
	require.Zero(t, actorRow.Cooldown)
	require.Nil(t, actorRow.CooldownUntil, "success also clears the actor identity used when expand could not resolve an inbox")
	kept := readDeliveryTarget(t, conn, other)
	require.Equal(t, int64((65*time.Minute)/time.Second), kept.Cooldown)

	again := Delivery{ActivityID: actor + "/activities/2", TargetInbox: inbox, Payload: string(payload), Status: DeliveryStatusPending}
	require.NoError(t, conn.Create(&again).Error)
	installCooldownTransport(t, func(r *http.Request) (*http.Response, error) {
		return outboundResponse(502, "down again"), nil
	})
	NewDeliveryQueue(nil, "", 0).processPending()
	restarted := readDeliveryTarget(t, conn, inbox)
	require.Equal(t, int64((5*time.Minute)/time.Second), restarted.Cooldown, "the next failure starts again at 5 minutes")
	require.Nil(t, restarted.CooldownUntil)
}

func TestDeliveryTargetCooldownCapsAtTwentyFourHours(t *testing.T) {
	conn := outboundDB(t)
	inbox := "https://capped.example/inbox"
	now := time.Now()
	require.NoError(t, conn.Create(&DeliveryTarget{
		Target:   inbox,
		Cooldown: int64((24*time.Hour - 5*time.Minute) / time.Second),
	}).Error)

	require.NoError(t, noteDeliveryTargetFailure(now, inbox))
	row := readDeliveryTarget(t, conn, inbox)
	require.Equal(t, int64((24*time.Hour)/time.Second), row.Cooldown, "a failure still adds 5 minutes up to the cap")
	require.NotNil(t, row.CooldownUntil)
	require.WithinDuration(t, now.Add(24*time.Hour), *row.CooldownUntil, time.Second)

	later := now.Add(time.Minute)
	require.NoError(t, noteDeliveryTargetFailure(later, inbox))
	row = readDeliveryTarget(t, conn, inbox)
	require.Equal(t, int64((24*time.Hour)/time.Second), row.Cooldown, "stored cooldown never exceeds 24 hours")
	require.NotNil(t, row.CooldownUntil)
	require.WithinDuration(t, later.Add(24*time.Hour), *row.CooldownUntil, time.Second)

	elapsed := time.Now().Add(-time.Second)
	require.NoError(t, conn.Model(&DeliveryTarget{}).Where("target = ?", inbox).Update("cooldown_until", elapsed).Error)
	delivery := newCooldownDelivery(t, conn, "capped_sender", inbox)
	installCooldownTransport(t, func(r *http.Request) (*http.Response, error) {
		require.Equal(t, inbox, r.URL.String())
		return outboundResponse(202, ""), nil
	})
	NewDeliveryQueue(nil, "", 0).processPending()
	require.NoError(t, conn.First(&delivery, delivery.ID).Error)
	require.Equal(t, DeliveryStatusDelivered, delivery.Status)
	cleared := readDeliveryTarget(t, conn, inbox)
	require.Zero(t, cleared.Cooldown, "a successful delivery still clears a capped cooldown to zero")
	require.Nil(t, cleared.CooldownUntil)
}

func TestExpandFailureAddsFiveMinuteCooldown(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.Relay{}))
	author := outboundAccount(t, conn, "writer", false)
	bad := outboundAccount(t, conn, "bad", true)
	require.NoError(t, conn.Create(&models.Follow{Id: 11, AccountId: bad.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	require.NoError(t, conn.Create(&models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "hello"}}).Error)
	installCooldownTransport(t, func(r *http.Request) (*http.Response, error) {
		return outboundResponse(502, "bad gateway"), nil
	})

	(&OutboundWorker{}).processPending()
	row := readDeliveryTarget(t, conn, bad.OriginId)
	require.Equal(t, int64((5*time.Minute)/time.Second), row.Cooldown)
	require.Nil(t, row.CooldownUntil)
	events := outboxEvents(t, conn)
	require.Equal(t, "processed", events[0].State)
}

func TestExpandWithinGraceStillFetchesTarget(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.Relay{}))
	author := outboundAccount(t, conn, "writer", false)
	bad := outboundAccount(t, conn, "bad", true)
	require.NoError(t, conn.Create(&models.Follow{Id: 21, AccountId: bad.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	require.NoError(t, conn.Create(&models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "still try"}}).Error)
	now := time.Now()
	for range 12 {
		require.NoError(t, noteDeliveryTargetFailure(now, bad.OriginId))
	}
	aged := now.Add(-72 * time.Hour)
	require.NoError(t, conn.Model(&models.FederationOutbox{}).Where("account_id = ?", author.Id).Update("created_at", aged).Error)

	gets := 0
	installCooldownTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			gets++
			require.Equal(t, bad.OriginId, r.URL.String())
			return outboundResponse(200, `{"id":"`+bad.OriginId+`","inbox":"`+bad.OriginId+`/inbox"}`), nil
		}
		return outboundResponse(202, ""), nil
	})
	(&OutboundWorker{}).processPending()
	require.GreaterOrEqual(t, gets, 1, "an accumulated cooldown of 60 minutes still includes the target in expand")
	var deliveries []Delivery
	require.NoError(t, conn.Find(&deliveries).Error)
	require.Len(t, deliveries, 1)
	require.Equal(t, bad.OriginId+"/inbox", deliveries[0].TargetInbox)
	require.Equal(t, DeliveryStatusPending, deliveries[0].Status)
}

func TestExpandBeyondGraceSkipsTargetUntilElapsed(t *testing.T) {
	conn := outboundDB(t)
	require.NoError(t, conn.AutoMigrate(&models.Relay{}))
	author := outboundAccount(t, conn, "writer", false)
	good := outboundAccount(t, conn, "good", true)
	bad := outboundAccount(t, conn, "bad", true)
	require.NoError(t, conn.Create(&models.Follow{Id: 31, AccountId: good.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	require.NoError(t, conn.Create(&models.Follow{Id: 32, AccountId: bad.Id, FollowId: author.Id, Status: models.FollowStatusFollowed}).Error)
	require.NoError(t, conn.Create(&models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "skip dead"}}).Error)
	now := time.Now()
	for range 13 {
		require.NoError(t, noteDeliveryTargetFailure(now, bad.OriginId))
	}
	before := readDeliveryTarget(t, conn, bad.OriginId)

	var fetched []string
	installCooldownTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			fetched = append(fetched, r.URL.String())
			return outboundResponse(200, `{"id":"`+r.URL.String()+`","inbox":"`+r.URL.String()+`/inbox"}`), nil
		}
		return outboundResponse(202, ""), nil
	})
	(&OutboundWorker{}).processPending()
	for _, uri := range fetched {
		require.NotEqual(t, bad.OriginId, uri, "expand must not fetch a target inside its cooldown window")
	}
	require.Contains(t, fetched, good.OriginId)
	after := readDeliveryTarget(t, conn, bad.OriginId)
	require.Equal(t, before.Cooldown, after.Cooldown, "a skip is not another failure")

	var skipped Delivery
	require.NoError(t, conn.Where("target_inbox = ?", bad.OriginId).First(&skipped).Error)
	require.Equal(t, DeliveryStatusFailed, skipped.Status)
	require.Contains(t, skipped.LastError, "cooling down")

	var queued Delivery
	require.NoError(t, conn.Where("target_inbox = ?", good.OriginId+"/inbox").First(&queued).Error)
	require.Equal(t, DeliveryStatusPending, queued.Status)

	elapsed := time.Now().Add(-time.Second)
	require.NoError(t, conn.Model(&DeliveryTarget{}).Where("target = ?", bad.OriginId).Update("cooldown_until", elapsed).Error)
	require.NoError(t, conn.Create(&models.Status{AccountId: author.Id, Visibility: models.StatusVisibilityPublic, Content: models.StatusContent{Content: "after window"}}).Error)
	fetched = nil
	(&OutboundWorker{}).processPending()
	require.Contains(t, fetched, bad.OriginId, "expand includes the target again once the window has elapsed")
}

func newCooldownDelivery(t *testing.T, conn *gorm.DB, authorName, inbox string) Delivery {
	t.Helper()
	author := outboundAccount(t, conn, authorName, false)
	actor := systemsettings.GetBaseURL() + "/users/" + author.Username
	payload, err := json.Marshal(map[string]any{"id": actor + "/activities/1", "actor": actor, "type": "Create", "object": "https://social.example/notes/1"})
	require.NoError(t, err)
	delivery := Delivery{ActivityID: actor + "/activities/1", TargetInbox: inbox, Payload: string(payload), Status: DeliveryStatusPending}
	require.NoError(t, conn.Create(&delivery).Error)
	return delivery
}

func readDeliveryTarget(t *testing.T, conn *gorm.DB, target string) DeliveryTarget {
	t.Helper()
	var row DeliveryTarget
	require.NoError(t, conn.Where("target = ?", target).First(&row).Error)
	return row
}

func installCooldownTransport(t *testing.T, rt outboundTransport) {
	t.Helper()
	old := sharedHTTPClient
	t.Cleanup(func() { sharedHTTPClient = old })
	sharedHTTPClient = &http.Client{Transport: rt}
}
