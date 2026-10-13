package homeserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	gmsl "github.com/matrix-org/gomatrixserverlib"
	"github.com/matrix-org/gomatrixserverlib/spec"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Knock previews are not a verified room DAG. Never author messages from this
// state or mistake a submitted knock for having joined the room.
const knockPreviewPrefix = "knock|"

func knockPreviewServer(room *MatrixRoom) string {
	if room.CurrentStateID != nil && strings.HasPrefix(*room.CurrentStateID, knockPreviewPrefix) {
		return strings.TrimPrefix(*room.CurrentStateID, knockPreviewPrefix)
	}
	return ""
}

type knockStrippedEvent struct {
	Type     EventType      `json:"type"`
	StateKey string         `json:"state_key"`
	Sender   string         `json:"sender"`
	Content  map[string]any `json:"content"`
}

func (c *FederationClient) MakeKnockContext(ctx context.Context, server, roomID, user string) (map[string]any, error) {
	endpoint := fmt.Sprintf("%s/_matrix/federation/v1/make_knock/%s/%s?%s", c.federationBaseURL(server), url.PathEscape(roomID), url.PathEscape(user), makeJoinVersionQuery())
	response, body, err := c.doFederationRequestContext(ctx, http.MethodGet, endpoint, server, nil, 1<<20)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		return nil, &federationHTTPError{Status: response.StatusCode, Message: string(body)}
	}
	var out map[string]any
	err = json.Unmarshal(body, &out)
	return out, err
}
func (c *FederationClient) SendKnockContext(ctx context.Context, server string, p gmsl.PDU) ([]knockStrippedEvent, error) {
	wire, err := wirePDU(p)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(p.EventID(), ":") {
		delete(wire, "event_id")
	}
	endpoint := fmt.Sprintf("%s/_matrix/federation/v1/send_knock/%s/%s", c.federationBaseURL(server), url.PathEscape(p.RoomID().String()), url.PathEscape(p.EventID()))
	response, body, err := c.doFederationRequestContext(ctx, http.MethodPut, endpoint, server, wire, 1<<20)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		return nil, &federationHTTPError{Status: response.StatusCode, Message: string(body)}
	}
	var out struct {
		State []knockStrippedEvent `json:"knock_room_state"`
	}
	if err = json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if len(out.State) > 100 {
		return nil, errors.New("knock preview too large")
	}
	return out.State, nil
}

func (r *RoomService) KnockRoomContext(ctx context.Context, user, target string, via []string, reason string) (string, *MatrixError) {
	settings := joinSettingsSource()
	ctx, cancel := context.WithTimeout(ctx, settings.Total)
	defer cancel()
	roomID, servers, merr := resolveWebGroupTarget(ctx, r.HS, target, via)
	if merr != nil {
		return "", merr
	}
	conn := db.GetDB().WithContext(ctx)
	var room MatrixRoom
	err := conn.Where("room_id = ?", roomID).First(&room).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrInternal
	}
	var member MatrixRoomMember
	if conn.Where("room_id = ? AND user_id = ?", roomID, user).First(&member).Error == nil {
		switch member.Membership {
		case MembershipKnock, MembershipJoin:
			return roomID, nil
		case MembershipBan, MembershipInvite:
			return "", ErrForbidden
		}
	}
	// Locally created rooms can authorize a signed knock. A room merely known
	// because another local user joined it must still confirm the request with
	// a resident remote server before reporting a pending application.
	localCreator := extractServerName(room.Creator) == r.HS.Config.ServerName
	if err == nil && localCreator && knockPreviewServer(&room) == "" {
		var create MatrixEvent
		if conn.Where("room_id = ? AND event_type = ?", roomID, EventTypeRoomCreate).First(&create).Error == nil && create.FederationJSON != "" {
			if room.JoinRule != "knock" && room.JoinRule != "knock_restricted" {
				return "", ErrForbidden
			}
			event := &MatrixEvent{RoomID: roomID, EventType: EventTypeRoomMember, Sender: user, StateKey: &user, Content: map[string]any{"membership": "knock"}}
			if reason != "" {
				event.Content["reason"] = reason
			}
			err := conn.Transaction(func(tx *gorm.DB) error {
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("room_id = ?", roomID).First(&room).Error; err != nil {
					return err
				}
				if err := buildLocalPDU(tx, r.HS, event, room.RoomVersion); err != nil {
					return err
				}
				p, err := parseAndValidatePDU(room.RoomVersion, []byte(event.FederationJSON))
				if err != nil {
					return err
				}
				if e := persistVerifiedPDU(tx, r.HS, p, true); e != nil {
					return e
				}
				return r.HS.queueNativeFederation(tx, event)
			})
			if err != nil {
				return "", ErrForbidden
			}
			go r.HS.NotifyRoomEvent(roomID)

			return roomID, nil
		}
	}
	hints := append(servers, knockPreviewServer(&room), extractServerName(room.Creator))
	if err == nil {
		hints = append(hints, r.remoteLeaveCandidates(roomID, &member, &room)...)
	}
	candidates := selectJoinCandidates(orderJoinServers(r.HS.Config.ServerName, roomID, hints, nil, ""), settings)
	if len(candidates) == 0 {
		return "", &MatrixError{ErrCode: "M_INVALID_PARAM", Error: "A room server hint is required"}
	}
	gate := joinGate(settings.MaxConcurrent)
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return "", joinContextError(ctx, 0, settings.Total)
	}
	client := newFederationClient(r.HS)
	var last error
	for _, server := range candidates {
		if ctx.Err() != nil {
			return "", joinContextError(ctx, 0, settings.Total)
		}
		attempt, cancelAttempt := context.WithTimeout(ctx, settings.MakeJoinTimeout)
		response, e := client.MakeKnockContext(attempt, server, roomID, user)
		cancelAttempt()
		if e != nil {
			last = e
			continue
		}
		version, _ := response["room_version"].(string)
		impl, e := gmsl.GetRoomVersion(gmsl.RoomVersion(version))
		if e != nil {
			return "", &MatrixError{ErrCode: "M_INCOMPATIBLE_ROOM_VERSION", Error: "Unsupported room version"}
		}
		raw, e := json.Marshal(response["event"])
		if e != nil {
			return "", ErrInternal
		}
		builder := impl.NewEventBuilder()
		if json.Unmarshal(raw, builder) != nil {
			return "", ErrInternal
		}
		// A foreign server supplies only DAG references. Identity and membership
		// content are ours and cannot be changed by an untrusted template.
		builder.RoomID, builder.Type, builder.SenderID, builder.StateKey = roomID, "m.room.member", user, &user
		content := map[string]any{"membership": "knock"}
		if reason != "" {
			content["reason"] = reason
		}
		if e = builder.SetContent(content); e != nil {
			return "", ErrInternal
		}
		p, e := builder.Build(time.Now(), spec.ServerName(r.HS.Config.ServerName), gmsl.KeyID(r.HS.keyID), r.HS.privateKey)
		if e != nil {
			return "", ErrInternal
		}
		state, e := client.SendKnockContext(ctx, server, p)
		if e != nil {
			// A timeout after submission is uncertain. Do not send a different event
			// to another server, and never report a pending request without acceptance.
			return "", &MatrixError{ErrCode: "M_BAD_GATEWAY", Error: "Unable to confirm the join request; retry after checking room state"}
		}
		if e = r.persistKnockPreview(ctx, server, p, state); e != nil {
			return "", ErrInternal
		}
		go r.HS.NotifyRoomEvent(roomID)
		return roomID, nil
	}
	if last != nil {
		return "", federationJoinError(last)
	}
	return "", ErrNotFound
}

func (r *RoomService) persistKnockPreview(ctx context.Context, server string, p gmsl.PDU, state []knockStrippedEvent) error {
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var room MatrixRoom
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("room_id = ?", p.RoomID().String()).First(&room).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		fresh := errors.Is(err, gorm.ErrRecordNotFound)
		if fresh {
			room = MatrixRoom{RoomID: p.RoomID().String(), RoomVersion: string(p.Version()), JoinRule: "knock", Visibility: "private", HistoryVisibility: "joined", GuestAccess: "forbidden"}
		}
		// Do not overwrite a complete room if another local user already joined it.
		var n int64
		if err = tx.Model(&MatrixRoomState{}).Where("room_id = ? AND event_type = ?", room.RoomID, EventTypeRoomCreate).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			marker := knockPreviewPrefix + server
			room.CurrentStateID = &marker
			for _, s := range state {
				if s.StateKey != "" {
					continue
				}
				str := func(key string) *string {
					v, ok := s.Content[key].(string)
					if !ok || len(v) > 1024 {
						return nil
					}
					return &v
				}
				switch s.Type {
				case EventTypeRoomName:
					room.Name = str("name")
				case EventTypeRoomTopic:
					room.Topic = str("topic")
				case EventTypeRoomAvatar:
					room.AvatarURL = str("url")
				case EventTypeRoomJoinRules:
					if v := str("join_rule"); v != nil {
						room.JoinRule = *v
					}
				case EventTypeRoomEncryption:
					room.IsEncrypted = true
				case EventTypeRoomCreate:
					room.RoomType = str("type")
				}
			}
			if fresh {
				if err = tx.Create(&room).Error; err != nil {
					return err
				}
			} else if err = tx.Save(&room).Error; err != nil {
				return err
			}
		}
		event := pduToMatrixEvent(p)
		if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(event).Error; err != nil {
			return err
		}
		var member MatrixRoomMember
		err = tx.Where("room_id = ? AND user_id = ?", room.RoomID, string(p.SenderID())).First(&member).Error
		missing := errors.Is(err, gorm.ErrRecordNotFound)
		if err != nil && !missing {
			return err
		}
		// An invite, join or ban received while send_knock was in flight is
		// newer authoritative membership. Never replace it with our preview.
		if !missing && (member.Membership == MembershipJoin || member.Membership == MembershipInvite || member.Membership == MembershipBan) {
			return nil
		}
		if err = tx.Where("room_id = ? AND event_type = ? AND state_key = ?", room.RoomID, EventTypeRoomMember, string(p.SenderID())).Delete(&MatrixRoomState{}).Error; err != nil {
			return err
		}
		if err = tx.Create(&MatrixRoomState{RoomID: room.RoomID, EventType: EventTypeRoomMember, StateKey: string(p.SenderID()), EventID: p.EventID(), Content: event.Content}).Error; err != nil {
			return err
		}
		if missing {
			return tx.Create(&MatrixRoomMember{RoomID: room.RoomID, UserID: string(p.SenderID()), Membership: MembershipKnock, EventID: p.EventID()}).Error
		}
		return tx.Model(&member).Updates(map[string]any{"membership": MembershipKnock, "event_id": p.EventID(), "forgotten": false}).Error
	})
}

func handleMakeKnock(c *gin.Context) {
	hs := GetHomeServer()
	roomID, user := c.Param("roomId"), c.Param("userId")
	if extractServerName(user) != c.GetString("origin") {
		federationFailure(c, 403, errors.New("origin mismatch"))
		return
	}
	version, err := federationRoomVersion(roomID)
	if err != nil {
		federationFailure(c, 404, err)
		return
	}
	compatible := false
	for _, v := range c.QueryArray("ver") {
		if v == version {
			compatible = true
		}
	}
	if !compatible {
		c.JSON(400, gin.H{"errcode": "M_INCOMPATIBLE_ROOM_VERSION", "room_version": version, "error": "Room version not supported"})
		return
	}
	event := &MatrixEvent{RoomID: roomID, Sender: user, StateKey: &user, EventType: EventTypeRoomMember, Content: map[string]any{"membership": "knock"}}
	if err = buildLocalPDU(db.GetDB(), hs, event, version); err != nil {
		federationFailure(c, 403, err)
		return
	}
	wire, err := matrixEventToPDU(event)
	if err != nil {
		federationFailure(c, 500, err)
		return
	}
	delete(wire, "signatures")
	delete(wire, "hashes")
	c.JSON(200, gin.H{"room_version": version, "event": wire})
}
func handleSendKnock(c *gin.Context) {
	hs := GetHomeServer()
	roomID := c.Param("roomId")
	version, err := federationRoomVersion(roomID)
	if err != nil {
		federationFailure(c, 404, err)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		federationFailure(c, 400, errors.New("invalid knock body"))
		return
	}
	p, err := parseAndValidatePDU(version, raw)
	if err != nil {
		federationFailure(c, 400, err)
		return
	}
	membership, _ := p.Membership()
	if p.RoomID().String() != roomID || p.EventID() != c.Param("eventId") || p.Type() != "m.room.member" || p.StateKey() == nil || *p.StateKey() != string(p.SenderID()) || membership != "knock" || extractServerName(string(p.SenderID())) != c.GetString("origin") {
		federationFailure(c, 403, errors.New("knock identity mismatch"))
		return
	}
	if err = validateIncomingPDU(c.Request.Context(), hs, p, true); err != nil {
		federationFailure(c, 403, err)
		return
	}
	// Return only the preview needed to identify the room, not its membership list.
	var states []MatrixRoomState
	if err = db.GetDB().Where("room_id = ? AND state_key = ? AND event_type IN ?", roomID, "", []EventType{EventTypeRoomCreate, EventTypeRoomName, EventTypeRoomTopic, EventTypeRoomAvatar, EventTypeRoomCanonicalAlias, EventTypeRoomJoinRules, EventTypeRoomEncryption}).Find(&states).Error; err != nil {
		federationFailure(c, 500, err)
		return
	}
	preview := []knockStrippedEvent{}
	for _, s := range states {
		var event MatrixEvent
		if db.GetDB().Select("sender").Where("event_id = ?", s.EventID).First(&event).Error != nil {
			continue
		}
		preview = append(preview, knockStrippedEvent{Type: s.EventType, StateKey: s.StateKey, Sender: event.Sender, Content: s.Content})
	}
	if err = db.GetDB().Transaction(func(tx *gorm.DB) error {
		if e := persistVerifiedPDU(tx, hs, p, true); e != nil {
			return e
		}
		return hs.queueNativeFederation(tx, pduToMatrixEvent(p))
	}); err != nil {
		federationFailure(c, 500, err)
		return
	}
	go hs.NotifyRoomEvent(roomID)

	c.JSON(200, gin.H{"knock_room_state": preview})
}
