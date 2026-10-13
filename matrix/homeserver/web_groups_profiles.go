package homeserver

import (
	"encoding/base64"
	"strings"

	"github.com/kasuganosora/community/defaultavatar"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// Prefer the room's own profile. Native member persistence does not always copy
// displayname/avatar_url into denormalized member columns, so read current
// membership state in one batch and use local account profiles only as fallback.
func webGroupMemberProfiles(hs *HomeServer, conn *gorm.DB, roomID string, members []MatrixRoomMember) (map[string]webGroupSender, error) {
	result := map[string]webGroupSender{}
	if len(members) == 0 {
		return result, nil
	}
	ids := make([]string, 0, len(members))
	locals := []string{}
	for _, m := range members {
		ids = append(ids, m.UserID)
		local, server, valid := ParseMatrixUserID(m.UserID)
		if valid && server == hs.Config.ServerName {
			locals = append(locals, local)
		}
	}
	var states []MatrixRoomState
	if err := conn.Where("room_id = ? AND event_type = ? AND state_key IN ?", roomID, EventTypeRoomMember, ids).Find(&states).Error; err != nil {
		return nil, err
	}
	roomProfiles := map[string]map[string]any{}
	for _, state := range states {
		roomProfiles[state.StateKey] = state.Content
	}
	accounts := map[string]models.Account{}
	if len(locals) > 0 {
		var rows []models.Account
		if err := conn.Select("username", "display_name").Where("username IN ? AND domain IS NULL", locals).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, account := range rows {
			accounts[FormatMatrixUserID(account.Username, hs.Config.ServerName)] = account
		}
	}
	for _, m := range members {
		profile := webGroupSender{ID: m.UserID, DisplayName: m.UserID, AvatarURL: defaultavatar.Path(defaultavatar.Seed(m.UserID, "matrix"))}
		local, hasLocal := accounts[m.UserID]
		if hasLocal {
			profile.DisplayName = local.DisplayName
			if profile.DisplayName == "" {
				profile.DisplayName = local.Username
			}
		}
		if m.DisplayName != nil && *m.DisplayName != "" {
			profile.DisplayName = *m.DisplayName
		}
		if name, ok := roomProfiles[m.UserID]["displayname"].(string); ok && name != "" {
			profile.DisplayName = name
		}
		avatar, _ := roomProfiles[m.UserID]["avatar_url"].(string)
		if avatar == "" && m.AvatarURL != nil {
			avatar = *m.AvatarURL
		}
		if hasLocal || strings.HasPrefix(avatar, "mxc://") {
			profile.AvatarURL = "/api/v1/chat/groups/" + webGroupID(roomID) + "/members/" + base64.RawURLEncoding.EncodeToString([]byte(m.UserID)) + "/avatar"
		}
		result[m.UserID] = profile
	}
	return result, nil
}
