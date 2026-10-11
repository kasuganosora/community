package gomatrixserverlib

import (
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/matrix-org/gomatrixserverlib/spec"
	"github.com/stretchr/testify/require"
)

// A v12 ban/join conflict needs the immutable creator identity during control
// ordering, before partial state is populated. Auth fallback must then use the
// declared auth event, not insert the candidate event into its own auth state.
func TestCommunityV12ConflictedBanUsesCreatorAndDeclaredAuth(t *testing.T) {
	version, err := GetRoomVersion(RoomVersionV12)
	require.NoError(t, err)
	signer := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	userIDForSender := func(_ spec.RoomID, sender spec.SenderID) (*spec.UserID, error) {
		return spec.NewUserID(string(sender), true)
	}
	roomID := ""
	var state, all []PDU
	add := func(sender, kind, key string, content map[string]any) PDU {
		provider, authErr := NewAuthEvents(state)
		require.NoError(t, authErr)
		builder := version.NewEventBuilder()
		builder.RoomID = roomID
		builder.SenderID = sender
		builder.Type = kind
		builder.StateKey = &key
		builder.Depth = int64(len(all) + 1)
		if len(all) > 0 {
			builder.PrevEvents = []string{all[len(all)-1].EventID()}
		}
		require.NoError(t, builder.SetContent(content))
		require.NoError(t, builder.AddAuthEvents(provider))
		event, buildErr := builder.Build(time.Unix(1700000000+int64(len(all)), 0), "example.com", "ed25519:test", signer)
		require.NoError(t, buildErr)
		require.NoError(t, Allowed(event, provider, userIDForSender))
		all = append(all, event)
		return event
	}
	alice, bob := "@alice:example.com", "@bob:example.com"
	create := add(alice, spec.MRoomCreate, "", map[string]any{"room_version": "12"})
	roomID = create.RoomID().String()
	state = append(state, create)
	state = append(state, add(alice, spec.MRoomMember, alice, map[string]any{"membership": "join"}))
	state = append(state, add(alice, spec.MRoomJoinRules, "", map[string]any{"join_rule": "public"}))
	joined := add(bob, spec.MRoomMember, bob, map[string]any{"membership": "join"})
	state = append(state, joined)
	ban := add(alice, spec.MRoomMember, bob, map[string]any{"membership": "ban"})
	topic := add(alice, spec.MRoomTopic, "", map[string]any{"topic": "concurrent branch"})
	left := append([]PDU{}, state...)
	for i, event := range left {
		if event.EventID() == joined.EventID() {
			left[i] = ban
		}
	}
	right := append(append([]PDU{}, state...), topic)
	resolved, err := ResolveConflictsNew(RoomVersionV12, [][]PDU{left, right}, all, userIDForSender, func(string) bool { return false })
	require.NoError(t, err)
	var member PDU
	for _, event := range resolved {
		if event.Type() == spec.MRoomMember && event.StateKeyEquals(bob) {
			member = event
		}
	}
	require.NotNil(t, member)
	require.Equal(t, ban.EventID(), member.EventID(), "ban must win over the other branch's old joined membership")
}
