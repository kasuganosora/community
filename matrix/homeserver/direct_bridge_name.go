package homeserver

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"golang.org/x/net/idna"
	"gorm.io/gorm"
)

const bridgeGeneratedNameKey = "org.community.generated_name"

type bridgeNameMember struct {
	id, label string
	local     bool
}

func bridgeNameText(value string, limit int) string {
	value = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, value))
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return value
}

func bridgeParticipantTitle(members []bridgeNameMember) string {
	sort.Slice(members, func(i, j int) bool {
		if members[i].local != members[j].local {
			return !members[i].local
		}
		return members[i].id < members[j].id
	})
	if len(members) == 2 && members[0].local != members[1].local {
		return members[0].label
	}
	if len(members) == 1 {
		return members[0].label
	}
	labels := []string{}
	for i, m := range members {
		if i == 3 {
			break
		}
		labels = append(labels, m.label)
	}
	if len(labels) == 0 {
		return "私信"
	}
	if len(members) == 2 {
		return strings.Join(labels, " · ")
	}
	return fmt.Sprintf("群聊 · %s（%d 人）", strings.Join(labels, "、"), len(members))
}

// Only bridge-owned names are maintained. A human-selected room name remains
// authoritative, even when the participants or their profile names change.
func (hs *HomeServer) syncBridgeRoomName(ctx context.Context, roomID string) error {
	conn := db.GetDB().WithContext(ctx)
	var room MatrixRoom
	if err := conn.First(&room, "room_id = ?", roomID).Error; err != nil {
		return err
	}
	var current MatrixRoomState
	err := conn.Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, "m.room.name", "").First(&current).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	name, _ := current.Content["name"].(string)
	generated, _ := current.Content[bridgeGeneratedNameKey].(string)
	if name != "" && name != "Fediverse private messages" && name != "Fediverse conversation" && generated != name {
		return nil
	}
	accounts := map[string]models.Account{}
	var conv BridgeConversation
	err = conn.First(&conv, "room_id = ?", roomID).Error
	if err == nil {
		if room.Creator != conv.Controller {
			return errDirectMessageSkipped
		}
		var members []BridgeConversationMember
		if err = conn.Where("conversation_id = ? AND left_revision = 0", conv.ID).Find(&members).Error; err != nil {
			return err
		}
		ids := []uint64{}
		for _, m := range members {
			if m.AccountID != 0 {
				ids = append(ids, m.AccountID)
			}
		}
		var rows []models.Account
		if len(ids) > 0 {
			if err = conn.Where("id IN ?", ids).Find(&rows).Error; err != nil {
				return err
			}
		}
		byID := map[uint64]models.Account{}
		for _, a := range rows {
			byID[a.Id] = a
		}
		for _, m := range members {
			if a, ok := byID[m.AccountID]; ok {
				accounts[m.MatrixID] = a
			}
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		suffix := ":" + hs.Config.ServerName
		hash := strings.TrimSuffix(strings.TrimPrefix(roomID, "!apdm_"), suffix)
		if !strings.HasPrefix(roomID, "!apdm_") || len(hash) != 64 || room.Creator != "@_ap_bridge_"+hash+suffix {
			return errDirectMessageSkipped
		}
		var mapping models.MatrixDMDelivery
		if err = conn.Where("room_id = ? AND state = ?", roomID, "delivered").Order("status_id DESC").First(&mapping).Error; err != nil {
			return err
		}
		var status models.Status
		if err = conn.Unscoped().Preload("Account").Preload("ToAccounts").First(&status, mapping.StatusID).Error; err != nil {
			return err
		}
		if status.Account != nil {
			accounts[hs.bridgeAccountID(*status.Account)] = *status.Account
		}
		for _, a := range status.ToAccounts {
			accounts[hs.bridgeAccountID(*a)] = *a
		}
	} else {
		return err
	}
	var joined []MatrixRoomMember
	if err = conn.Where("room_id = ? AND membership IN ?", roomID, []MembershipState{MembershipJoin, MembershipInvite}).Find(&joined).Error; err != nil {
		return err
	}
	names := []bridgeNameMember{}
	for _, m := range joined {
		if m.UserID == room.Creator {
			continue
		}
		_, server, ok := ParseMatrixUserID(m.UserID)
		if !ok {
			continue
		}
		acct, display, local := m.UserID, "", false
		if m.DisplayName != nil {
			display = *m.DisplayName
		}
		if server == hs.Config.ServerName {
			a, ok := accounts[m.UserID]
			if !ok {
				continue
			}
			acct = "@" + a.Username + "@" + hs.Config.ServerName
			display = a.DisplayName
			local = a.Domain == nil
			if a.Domain != nil {
				domain := *a.Domain
				if decoded, err := idna.Lookup.ToUnicode(domain); err == nil {
					domain = decoded
				}
				acct = "@" + a.Username + "@" + domain
			}
		}
		acct = bridgeNameText(acct, 100)
		display = bridgeNameText(display, 24)
		label := acct
		if display != "" && display != acct {
			label = display + "（" + acct + "）"
		}
		names = append(names, bridgeNameMember{id: m.UserID, label: label, local: local})
	}
	if len(names) == 0 {
		return nil
	}
	desired := bridgeNameText(bridgeParticipantTitle(names), 255)
	if name == desired {
		return nil
	}
	key := ""
	_, me := hs.Event.SendRoomEvent(roomID, room.Creator, "m.room.name", nil, map[string]any{"name": desired, bridgeGeneratedNameKey: desired}, &key)
	if me != nil {
		return errors.New(me.Error)
	}
	return nil
}

func (hs *HomeServer) refreshExplicitBridgeRoomNames(ctx context.Context) {
	after := ""
	for ctx.Err() == nil {
		var rows []BridgeConversation
		if err := db.GetDB().WithContext(ctx).Where("id > ? AND state = ?", after, "active").Order("id").Limit(50).Find(&rows).Error; err != nil {
			return
		}
		if len(rows) == 0 {
			return
		}
		for _, row := range rows {
			after = row.ID
			_ = hs.syncBridgeRoomName(ctx, row.RoomID)
		}
	}
}
