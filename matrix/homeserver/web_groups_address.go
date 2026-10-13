package homeserver

import (
	"errors"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

var webGroupAddressPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9_-]{0,62}[a-z0-9])?$`)

func webGroupLocallyCreated(hs *HomeServer, room *MatrixRoom) bool {
	_, server, valid := ParseMatrixUserID(room.Creator)
	return valid && server == hs.Config.ServerName
}

// A friendly address is a real Matrix alias. Claim the alias, change canonical
// room state and enqueue federation in one native-room transaction. Previous
// aliases are deliberately retained so existing shared links keep working.
func handleWebGroupAddress(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	var req struct {
		Alias string `json:"alias"`
	}
	if c.ShouldBindJSON(&req) != nil {
		webGroupError(c, ErrInvalidParam)
		return
	}
	slug := strings.ToLower(strings.TrimSpace(req.Alias))
	alias := "#" + slug + ":" + hs.Config.ServerName
	if !webGroupAddressPattern.MatchString(slug) || len(alias) > 255 {
		webGroupFailure(c, 400, "invalid_alias", "短地址需为 1–64 位字母、数字、下划线或连字符，首尾使用字母或数字")
		return
	}
	if !webGroupLocallyCreated(hs, room) || !hs.Event.canUserSendStateEvent(room.RoomID, user, EventTypeRoomCanonicalAlias) {
		webGroupError(c, ErrForbidden)
		return
	}
	if hs.AppService != nil {
		if _, reserved := hs.AppService.IsExclusiveAlias(alias); reserved {
			webGroupFailure(c, 409, "alias_taken", "此短地址已被占用，请换一个")
			return
		}
	}
	_, failure := webGroupPersistState(c.Request.Context(), hs, room.RoomID, user, string(EventTypeRoomCanonicalAlias), "", "", func(tx *gorm.DB, locked *MatrixRoom, states map[EventType]MatrixRoomState) (map[string]any, *MatrixError) {
		if webGroupCapabilities(hs, locked, MembershipJoin, states, user)["manage_address"] != true {
			return nil, ErrForbidden
		}
		if err := claimRoomAlias(tx, alias, locked.RoomID, user); err != nil {
			if errors.Is(err, errRoomAliasTaken) {
				return nil, &MatrixError{ErrCode: "M_ROOM_IN_USE", Error: "Room alias is already in use"}
			}
			_ = c.Error(err)
			return nil, ErrInternal
		}
		content := map[string]any{}
		for key, value := range states[EventTypeRoomCanonicalAlias].Content {
			content[key] = value
		}
		content["alias"] = alias
		return content, nil
	})
	if failure != nil {
		if failure.ErrCode == "M_ROOM_IN_USE" {
			webGroupFailure(c, 409, "alias_taken", "此短地址已被占用，请换一个")
		} else {
			webGroupError(c, failure)
		}
		return
	}
	handleWebGroupGet(c)
}
