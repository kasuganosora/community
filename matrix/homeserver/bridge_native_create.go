package homeserver

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
)

// Standard Matrix clients use createRoom, not Community's conversation API.
// Selecting an AP identity in a plaintext private room opts that room into the
// same managed conversation path, including durable delivery and reply routing.
func (hs *HomeServer) createNativeAPConversation(creator string, req *CreateRoomRequest) (*CreateRoomResponse, *MatrixError, bool) {
	if req.bridgeRoomID != "" {
		return nil, nil, false
	}
	hasAP := false
	for _, id := range req.Invite {
		if strings.HasPrefix(id, "@_ap_") && strings.HasSuffix(id, ":"+hs.Config.ServerName) {
			hasAP = true
		}
	}
	if !hasAP {
		return nil, nil, false
	}
	fail := func(message string) (*CreateRoomResponse, *MatrixError, bool) {
		return nil, &MatrixError{ErrCode: "M_FORBIDDEN", Error: message}, true
	}
	if req.Visibility == "public" || req.Preset == "public_chat" || len(req.Invite3PID) != 0 || req.RoomAliasName != nil {
		return fail("ActivityPub conversations must be private rooms with explicit participants")
	}
	for key, value := range req.CreationContent {
		switch key {
		case "m.federate":
			if enabled, ok := value.(bool); !ok || !enabled {
				return fail("ActivityPub conversations require federation")
			}
		case "room_version": // Ignored here as required by Matrix createRoom.
		default:
			return fail("Unsupported creation_content for an ActivityPub conversation")
		}
	}
	if req.RoomVersion != "" && req.RoomVersion != "10" {
		return nil, &MatrixError{ErrCode: "M_UNSUPPORTED_ROOM_VERSION", Error: "ActivityPub conversations currently use room version 10"}, true
	}
	for _, state := range req.InitialState {
		if state.Type == string(EventTypeRoomEncryption) {
			return fail("ActivityPub users cannot receive encrypted messages. Create this conversation with encryption disabled.")
		}
		if state.Type == "m.room.join_rules" && state.Content["join_rule"] != "invite" {
			return fail("ActivityPub conversations must use invite-only membership")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn := db.GetDB().WithContext(ctx)
	inputs := make([]BridgeMemberInput, 0, len(req.Invite))
	for _, id := range req.Invite {
		account, err := hs.resolveBridgeAccount(conn, id)
		if err != nil {
			return fail("Unknown ActivityPub user")
		}
		if account != nil {
			inputs = append(inputs, BridgeMemberInput{AccountID: strconv.FormatUint(account.Id, 10)})
		} else {
			inputs = append(inputs, BridgeMemberInput{MatrixID: id})
		}
	}
	conv, err := hs.CreateBridgeConversation(ctx, creator, BridgeConversationRequest{TransactionID: "matrix-create-" + uuid.NewString(), PlaintextConsent: true, Members: inputs})
	if err != nil {
		return fail("Unable to create ActivityPub conversation; check participants and account blocks")
	}
	if err := conn.Model(&MatrixRoom{}).Where("room_id = ?", conv.RoomID).Update("is_direct", req.IsDirect).Error; err != nil {
		return nil, ErrInternal, true
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		key := ""
		if _, me := hs.Event.SendRoomEvent(conv.RoomID, conv.Controller, "m.room.name", nil, map[string]any{"name": bridgeNameText(*req.Name, 255)}, &key); me != nil {
			return nil, me, true
		}
	}
	// Populate profiles before returning: Element should show the remote person,
	// rather than an empty room while waiting for their first inbound message.
	var members []BridgeConversationMember
	if err := conn.Where("conversation_id = ? AND left_revision = 0", conv.ID).Find(&members).Error; err != nil {
		return &CreateRoomResponse{RoomID: conv.RoomID}, nil, true
	}
	budget, cancelAvatars := context.WithTimeout(ctx, 3*time.Second)
	defer cancelAvatars()
	for _, member := range members {
		if member.AccountID == 0 || member.Kind == "matrix" {
			continue
		}
		var account models.Account
		if err := conn.First(&account, member.AccountID).Error; err != nil {
			continue
		}
		// The room is already active. Cosmetic refresh must not turn success
		// into a creation error and cause clients to create duplicate rooms.
		_ = hs.syncBridgeProfile(ctx, budget, conv.RoomID, member.MatrixID, account)
	}
	return &CreateRoomResponse{RoomID: conv.RoomID}, nil, true
}
