package homeserver

import (
	"fmt"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/stretchr/testify/require"
)

func TestIncrementalSyncDepartureIgnoresIncludeLeaveFalse(t *testing.T) {
	for _, filter := range []string{
		`{"room":{"timeline":{"limit":20}}}`,
		`{"room":{"include_leave":false,"timeline":{"limit":0}}}`,
	} {
		t.Run(filter, func(t *testing.T) {
			hs := createTestHomeServer(t)
			user := "@alice:test.local"
			roomID := createPrivateRoom(t, hs, user)
			initial, err := hs.Sync.Sync(user, "DEVICE", &SyncRequest{Filter: filter})
			require.Nil(t, err)
			require.Contains(t, initial.Rooms.Join, roomID)
			require.Nil(t, hs.Room.LeaveRoom(user, roomID, &LeaveRoomRequest{}))

			response, err := hs.Sync.Sync(user, "DEVICE", &SyncRequest{Since: initial.NextBatch, Filter: filter})
			require.Nil(t, err)
			require.NotContains(t, response.Rooms.Join, roomID)
			require.NotContains(t, response.Rooms.Invite, roomID)
			require.Contains(t, response.Rooms.Leave, roomID)
			left := response.Rooms.Leave[roomID]
			events := append(left.State.Events, left.Timeline.Events...)
			found := false
			for _, event := range events {
				if event.Type == string(EventTypeRoomMember) && event.StateKey != nil && *event.StateKey == user && event.Content["membership"] == "leave" {
					found = true
				}
			}
			require.True(t, found, "the client must receive its own membership transition")

			var member MatrixRoomMember
			require.NoError(t, db.GetDB().First(&member, "room_id = ? AND user_id = ?", roomID, user).Error)
			var departure MatrixEvent
			require.NoError(t, db.GetDB().First(&departure, "event_id = ?", member.EventID).Error)
			next, err := hs.Sync.Sync(user, "DEVICE", &SyncRequest{Since: fmt.Sprintf("s%d", departure.CreatedAt.UnixMilli()+1), Filter: filter})
			require.Nil(t, err)
			require.Empty(t, next.Rooms.Leave, "unchanged left rooms must not be repeated")
			initialAgain, err := hs.Sync.Sync(user, "DEVICE", &SyncRequest{Filter: filter})
			require.Nil(t, err)
			require.Empty(t, initialAgain.Rooms.Leave, "include_leave=false still excludes historical rooms on initial sync")
		})
	}
}

func TestDepartureWakesAllDepartingUserDevices(t *testing.T) {
	hs := createTestHomeServer(t)
	user := "@alice:test.local"
	roomID := createPrivateRoom(t, hs, user)
	hs.SyncNotifier = NewSyncNotifier()
	var waiters []<-chan struct{}
	for _, device := range []string{"PHONE", "DESKTOP"} {
		require.NoError(t, db.GetDB().Create(&MatrixUserSession{UserID: user, DeviceID: device, AccessToken: "leave-" + device}).Error)
		waiters = append(waiters, hs.SyncNotifier.Wait(user, device))
	}
	require.Nil(t, hs.Room.LeaveRoom(user, roomID, &LeaveRoomRequest{}))
	for _, waiter := range waiters {
		select {
		case <-waiter:
		case <-time.After(time.Second):
			t.Fatal("the departing user's pending sync was not woken")
		}
	}
	joined, err := hs.Room.GetJoinedRooms(user)
	require.NoError(t, err)
	require.NotContains(t, joined, roomID)
}
