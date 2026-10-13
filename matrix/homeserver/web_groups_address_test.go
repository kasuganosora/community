package homeserver

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestWebGroupAddressLifecycleAndDirectory(t *testing.T) {
	hs, conn, router := setupWebGroupCore(t)
	id := webGroupTestCreate(t, router)
	roomID, _ := webGroupRoomID(id)
	status, out := webGroupTestRequest(t, router, "GET", "/chat/groups/"+id, "", "")
	require.Equal(t, 200, status, out)
	require.Equal(t, "", out["canonical_alias"])
	require.Equal(t, "test.local", out["address_server"])
	require.Equal(t, true, out["capabilities"].(map[string]any)["manage_address"])
	for _, input := range []string{" Team-1 ", "new_address", "team-1"} {
		status, out = webGroupTestRequest(t, router, "PUT", "/chat/groups/"+id+"/address", fmt.Sprintf(`{"alias":%q}`, input), "")
		require.Equal(t, 200, status, out)
		alias := "#" + strings.ToLower(strings.TrimSpace(input)) + ":test.local"
		require.Equal(t, alias, out["canonical_alias"])
		require.Equal(t, "https://matrix.to/#/"+url.PathEscape(alias), out["client_url"])
		var state MatrixRoomState
		require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, EventTypeRoomCanonicalAlias, "").Take(&state).Error)
		require.Equal(t, alias, state.Content["alias"])
		var event MatrixEvent
		require.NoError(t, conn.Where("event_id = ?", state.EventID).Take(&event).Error)
		require.NotEmpty(t, event.Signatures[hs.Config.ServerName], "canonical address must be a signed native event")
	}
	for _, alias := range []string{"#team-1:test.local", "#new_address:test.local"} {
		resolved, err := NewAliasService(hs).ResolveAliasContext(context.Background(), alias)
		require.Nil(t, err)
		require.Equal(t, roomID, resolved.RoomID)
	}
	router.GET("/federation/query/directory", handleQueryDirectory)
	router.GET("/client/directory/room/:roomAlias", handleGetRoomAlias)
	for _, path := range []string{"/federation/query/directory?room_alias=" + url.QueryEscape("#team-1:test.local"), "/client/directory/room/" + url.PathEscape("#team-1:test.local")} {
		status, out = webGroupTestRequest(t, router, "GET", path, "", "")
		require.Equal(t, 200, status, out)
		require.Equal(t, roomID, out["room_id"])
		require.NotContains(t, out, "name", "directory resolution must not reveal private room metadata")
	}
	status, out = webGroupTestRequest(t, router, "GET", "/chat/groups/search?scope=local&q=team", "", "@outsider:test.local")
	require.Equal(t, 200, status, out)
	require.Empty(t, out["groups"], "an address must not publish an invitation-only group")
	_, failure := hs.Room.CreateRoom("@bob:test.local", &CreateRoomRequest{Name: new("Conflicting native room"), Preset: "private_chat", RoomAliasName: new("team-1")})
	require.NotNil(t, failure)
	require.Equal(t, "M_ROOM_IN_USE", failure.ErrCode)
	var count int64
	require.NoError(t, conn.Model(&MatrixRoom{}).Where("name = ?", "Conflicting native room").Count(&count).Error)
	require.Zero(t, count, "failed native creation must not leave a room behind")
}

func TestWebGroupAddressValidationAuthorizationAndCollision(t *testing.T) {
	hs, conn, router := setupWebGroupCore(t)
	id := webGroupTestCreate(t, router)
	roomID, _ := webGroupRoomID(id)
	for _, alias := range []string{"", "_first", "last-", "has space", "has.dot", "#full:test.local", "../chat", strings.Repeat("a", 65)} {
		status, out := webGroupTestRequest(t, router, "PUT", "/chat/groups/"+id+"/address", fmt.Sprintf(`{"alias":%q}`, alias), "")
		require.Equal(t, 400, status, out)
		require.Equal(t, "invalid_alias", out["code"])
	}
	status, out := webGroupTestRequest(t, router, "PUT", "/chat/groups/"+id+"/address", `{"alias":"chat"}`, "@outsider:test.local")
	require.Equal(t, 403, status, out)
	status, out = webGroupTestRequest(t, router, "POST", "/chat/groups/"+id+"/members", `{"user_id":"bob","action":"invite"}`, "")
	require.Equal(t, 200, status, out)
	_, failure := hs.Room.JoinRoom("@bob:test.local", roomID, &JoinRoomRequest{})
	require.Nil(t, failure)
	status, out = webGroupTestRequest(t, router, "PUT", "/chat/groups/"+id+"/address", `{"alias":"chat"}`, "@bob:test.local")
	require.Equal(t, 403, status, out)
	status, out = webGroupTestRequest(t, router, "PUT", "/chat/groups/"+id+"/address", `{"alias":"chat"}`, "")
	require.Equal(t, 200, status, out)
	other, failure := hs.Room.CreateRoom("@bob:test.local", &CreateRoomRequest{Name: new("Other"), Preset: "private_chat"})
	require.Nil(t, failure)
	status, out = webGroupTestRequest(t, router, "PUT", "/chat/groups/"+webGroupID(other.RoomID)+"/address", `{"alias":"chat"}`, "@bob:test.local")
	require.Equal(t, 409, status, out)
	require.Equal(t, "alias_taken", out["code"])
	var claimed MatrixRoomAlias
	require.NoError(t, conn.Where("room_alias = ?", "#chat:test.local").Take(&claimed).Error)
	require.Equal(t, roomID, claimed.RoomID)
	// A native Matrix client cannot bypass the web path and seize the address.
	router.PUT("/client/directory/room/:roomAlias", func(c *gin.Context) { c.Set("user_id", "@bob:test.local"); handleSetRoomAlias(c) })
	status, out = webGroupTestRequest(t, router, "PUT", "/client/directory/room/"+url.PathEscape("#chat:test.local"), fmt.Sprintf(`{"room_id":%q}`, other.RoomID), "")
	require.Equal(t, 409, status, out)
	require.Equal(t, "M_ROOM_IN_USE", out["errcode"])
	// Room ownership is its creator's server, not the current administrator.
	require.NoError(t, conn.Model(&MatrixRoom{}).Where("room_id = ?", roomID).Update("creator", "@creator:remote.example").Error)
	status, out = webGroupTestRequest(t, router, "PUT", "/chat/groups/"+id+"/address", `{"alias":"remote"}`, "")
	require.Equal(t, 403, status, out)
	status, out = webGroupTestRequest(t, router, "GET", "/chat/groups/"+id, "", "")
	require.Equal(t, 200, status, out)
	require.Equal(t, false, out["capabilities"].(map[string]any)["manage_address"])
	require.NoError(t, conn.Model(&MatrixRoom{}).Where("room_id = ?", roomID).Update("creator", "@alice:test.local").Error)
	_, failure = hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.encryption", nil, map[string]any{"algorithm": "m.megolm.v1.aes-sha2"}, new(""))
	require.Nil(t, failure)
	status, out = webGroupTestRequest(t, router, "PUT", "/chat/groups/"+id+"/address", `{"alias":"encrypted"}`, "")
	require.Equal(t, 409, status, out)
	require.Equal(t, "encrypted", out["code"])
}

func TestWebGroupAddressAndFederationRollbackTogether(t *testing.T) {
	_, conn, router := setupWebGroupCore(t)
	id := webGroupTestCreate(t, router)
	roomID, _ := webGroupRoomID(id)
	require.NoError(t, conn.Create(&MatrixRoomMember{RoomID: roomID, UserID: "@remote:other.example", Membership: MembershipJoin}).Error)
	require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("web_address_outbox_failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "BridgeFederationDelivery" {
			tx.AddError(errors.New("injected federation outbox failure"))
		}
	}))
	t.Cleanup(func() { conn.Callback().Create().Remove("web_address_outbox_failure") })
	status, out := webGroupTestRequest(t, router, "PUT", "/chat/groups/"+id+"/address", `{"alias":"atomic"}`, "")
	require.Equal(t, 500, status, out)
	for _, model := range []any{&MatrixRoomAlias{}, &MatrixRoomState{}, &MatrixEvent{}} {
		var count int64
		query := conn.Model(model).Where("room_id = ?", roomID)
		if _, aliases := model.(*MatrixRoomAlias); !aliases {
			query = query.Where("event_type = ?", EventTypeRoomCanonicalAlias)
		}
		require.NoError(t, query.Count(&count).Error)
		require.Zero(t, count)
	}
	require.NoError(t, conn.Callback().Create().Remove("web_address_outbox_failure"))
	status, out = webGroupTestRequest(t, router, "PUT", "/chat/groups/"+id+"/address", `{"alias":"atomic"}`, "")
	require.Equal(t, 200, status, out)
	var state MatrixRoomState
	require.NoError(t, conn.Where("room_id = ? AND event_type = ?", roomID, EventTypeRoomCanonicalAlias).Take(&state).Error)
	var queued BridgeFederationDelivery
	require.NoError(t, conn.Where("event_id = ? AND server_name = ?", state.EventID, "other.example").Take(&queued).Error)
	require.True(t, queued.Native)
}

func TestCreateRoomAliasStateFailureRollsBack(t *testing.T) {
	hs, conn, _ := setupWebGroupCore(t)
	require.NoError(t, conn.Callback().Create().Before("gorm:create").Register("native_address_state_failure", func(tx *gorm.DB) {
		if state, ok := tx.Statement.Dest.(*MatrixRoomState); ok && state.EventType == EventTypeRoomCanonicalAlias {
			tx.AddError(errors.New("injected canonical alias state failure"))
		}
	}))
	t.Cleanup(func() { conn.Callback().Create().Remove("native_address_state_failure") })
	room, failure := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Name: new("Atomic alias"), Preset: "private_chat", RoomAliasName: new("atomic")})
	require.Nil(t, room)
	require.Equal(t, ErrInternal, failure)
	for _, model := range []any{&MatrixRoom{}, &MatrixRoomAlias{}, &MatrixRoomState{}, &MatrixEvent{}} {
		var count int64
		require.NoError(t, conn.Model(model).Count(&count).Error)
		require.Zero(t, count, "canonical state failure must not leave a room or alias behind")
	}
}

func TestWebGroupMySQLAddressConcurrentClaims(t *testing.T) {
	hs, conn, router := setupWebGroupCore(t)
	id := webGroupTestCreate(t, router)
	other, failure := hs.Room.CreateRoom("@alice:test.local", &CreateRoomRequest{Name: new("Second"), Preset: "private_chat"})
	require.Nil(t, failure)
	ids := []string{id, webGroupID(other.RoomID)}
	responses := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			request := httptest.NewRequest("PUT", "/chat/groups/"+id+"/address", strings.NewReader(`{"alias":"race"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			responses <- response
		}()
	}
	close(start)
	wg.Wait()
	close(responses)
	statuses := []int{}
	for response := range responses {
		require.Contains(t, []int{200, 409}, response.Code, response.Body.String())
		statuses = append(statuses, response.Code)
	}
	require.ElementsMatch(t, []int{200, 409}, statuses)
	var alias MatrixRoomAlias
	require.NoError(t, conn.Where("room_alias = ?", "#race:test.local").Take(&alias).Error)
	var states []MatrixRoomState
	require.NoError(t, conn.Where("event_type = ?", EventTypeRoomCanonicalAlias).Find(&states).Error)
	require.Len(t, states, 1)
	require.Equal(t, alias.RoomID, states[0].RoomID)
}

func TestWebGroupPostgresAddressClaims(t *testing.T) {
	conn := postgresUnreadTestDB(t)
	require.NoError(t, conn.Exec(`CREATE TEMP TABLE matrix_room_aliases (
		id BIGSERIAL PRIMARY KEY, room_alias VARCHAR(255) UNIQUE NOT NULL,
		room_id VARCHAR(255) NOT NULL, creator VARCHAR(255), created_at TIMESTAMPTZ
	) ON COMMIT DROP`).Error)
	require.NoError(t, claimRoomAlias(conn, "#chat:test.local", "!one:test.local", "@alice:test.local"))
	require.NoError(t, claimRoomAlias(conn, "#chat:test.local", "!one:test.local", "@bob:test.local"))
	require.ErrorIs(t, claimRoomAlias(conn, "#chat:test.local", "!two:test.local", "@bob:test.local"), errRoomAliasTaken)
	var alias MatrixRoomAlias
	require.NoError(t, conn.Where("room_alias = ?", "#chat:test.local").Take(&alias).Error)
	require.Equal(t, "!one:test.local", alias.RoomID)
	require.Equal(t, "@alice:test.local", alias.Creator)
}
