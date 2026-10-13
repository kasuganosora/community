package homeserver

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWebGroupStateFailuresRollBackEventAndDelivery(t *testing.T) {
	for _, operation := range []string{"state_delete", "state_insert", "room_update", "member_update", "member_insert"} {
		t.Run(operation, func(t *testing.T) {
			hs, conn, router := setupWebGroupCore(t)
			id := webGroupTestCreate(t, router)
			roomID, _ := webGroupRoomID(id)
			if operation == "member_update" {
				require.Nil(t, hs.Room.InviteUser(roomID, "@alice:test.local", &InviteUserRequest{UserID: "@bob:test.local"}))
				_, matrixErr := hs.Room.JoinRoom("@bob:test.local", roomID, &JoinRoomRequest{})
				require.Nil(t, matrixErr)
			}
			// An actual destination ensures successful mutations would enqueue a PDU.
			require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: roomID, UserID: "@resident:other.example", Membership: MembershipJoin}).Error)
			var original MatrixRoom
			require.NoError(t, conn.Where("room_id = ?", roomID).Take(&original).Error)
			var states []MatrixRoomState
			require.NoError(t, conn.Where("room_id = ?", roomID).Order("event_type, state_key").Find(&states).Error)
			count := func(model any) int64 {
				var n int64
				require.NoError(t, conn.Model(model).Count(&n).Error)
				return n
			}
			eventsBefore, deliveryBefore := count(&MatrixEvent{}), count(&BridgeFederationDelivery{})
			injected := false
			callback := func(tx *gorm.DB) {
				if tx.Statement.Schema == nil {
					return
				}
				name := tx.Statement.Schema.Name
				fail := (operation == "state_delete" || operation == "state_insert") && name == "MatrixRoomState"
				fail = fail || ((operation == "member_update" || operation == "member_insert") && name == "MatrixRoomMember")
				if operation == "room_update" && name == "MatrixRoom" {
					if updates, ok := tx.Statement.Dest.(map[string]any); ok {
						_, fail = updates["name"]
					}
				}
				if fail {
					injected = true
					tx.AddError(errors.New("injected native room state failure"))
				}
			}
			remove := func() {}
			switch operation {
			case "state_delete":
				require.NoError(t, conn.Callback().Delete().Before("gorm:delete").Register(operation, callback))
				remove = func() { conn.Callback().Delete().Remove(operation) }
			case "state_insert", "member_insert":
				require.NoError(t, conn.Callback().Create().Before("gorm:create").Register(operation, callback))
				remove = func() { conn.Callback().Create().Remove(operation) }
			default:
				require.NoError(t, conn.Callback().Update().Before("gorm:update").Register(operation, callback))
				remove = func() { conn.Callback().Update().Remove(operation) }
			}
			t.Cleanup(remove)
			method, path, body := "PATCH", "/chat/groups/"+id, `{"name":"Must roll back"}`
			if operation == "member_update" {
				method, path, body = "POST", path+"/members", `{"action":"kick","user_id":"@bob:test.local"}`
			} else if operation == "member_insert" {
				method, path, body = "POST", path+"/members", `{"action":"invite","user_id":"bob"}`
			}
			status, response := webGroupTestRequest(t, router, method, path, body, "")
			require.True(t, injected, "the requested database failure must actually be exercised")
			require.Equal(t, 500, status, response)
			require.Equal(t, eventsBefore, count(&MatrixEvent{}), "no signed event may survive an incomplete state mutation")
			require.Equal(t, deliveryBefore, count(&BridgeFederationDelivery{}), "no federation delivery may survive an incomplete state mutation")
			var after MatrixRoom
			require.NoError(t, conn.Where("room_id = ?", roomID).Take(&after).Error)
			require.Equal(t, original.Name, after.Name)
			require.Equal(t, original.WebStreamPosition, after.WebStreamPosition)
			var afterStates []MatrixRoomState
			require.NoError(t, conn.Where("room_id = ?", roomID).Order("event_type, state_key").Find(&afterStates).Error)
			require.Equal(t, states, afterStates, "deleting/replacing current state must roll back too")
			if operation == "member_update" {
				membership, err := hs.Room.GetUserMembership(roomID, "@bob:test.local")
				require.NoError(t, err)
				require.Equal(t, MembershipJoin, membership)
			} else if operation == "member_insert" {
				var member MatrixRoomMember
				require.ErrorIs(t, conn.Where("room_id = ? AND user_id = ?", roomID, "@bob:test.local").Take(&member).Error, gorm.ErrRecordNotFound)
				var job WebGroupInvitation
				require.NoError(t, conn.Where("room_id = ?", roomID).Take(&job).Error)
				require.Equal(t, "pending", job.State, "a failed invitation must remain retryable")
			}
		})
	}
}
