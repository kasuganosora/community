package homeserver

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"net/http"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
)

func installFakeRemoteMediaFetcher(t *testing.T, hs *HomeServer, h http.Handler) {
	t.Helper()
	f, _ := fakeFetcher(t, h)
	remoteFetcherMu.Lock()
	prev := remoteFetcherByHS[hs]
	remoteFetcherByHS[hs] = f
	remoteFetcherMu.Unlock()
	t.Cleanup(func() {
		remoteFetcherMu.Lock()
		if prev == nil {
			delete(remoteFetcherByHS, hs)
		} else {
			remoteFetcherByHS[hs] = prev
		}
		remoteFetcherMu.Unlock()
	})
}

func assertPlaceholderPNG(t *testing.T, code int, headers http.Header, body []byte) {
	t.Helper()
	require.Equal(t, http.StatusOK, code, string(body))
	require.Equal(t, "image/png", headers.Get("Content-Type"))
	require.Equal(t, "placeholder", headers.Get("X-Avatar-Fallback"))
	cfg, err := png.DecodeConfig(bytes.NewReader(body))
	require.NoError(t, err)
	require.Greater(t, cfg.Width, 0)
	require.Greater(t, cfg.Height, 0)
}

func TestRemoteGroupAndMemberAvatarsDegradeCleanlyWithinBound(t *testing.T) {
	hs, router, roomID := webGroupMediaTestHS(t)
	conn := db.GetDB()
	require.NoError(t, conn.AutoMigrate(&MatrixGroupHotScore{}, &models.DomainBlock{}, &models.Block{}))
	router.GET("/api/v1/explore/groups/:id/avatar", HandleExploreGroupAvatar)

	prevTimeout := remoteAvatarTimeout
	remoteAvatarTimeout = 120 * time.Millisecond
	t.Cleanup(func() { remoteAvatarTimeout = prevTimeout })

	emptyKey := ""
	remoteMember := "@carol:remote.example"
	require.NoError(t, conn.Model(&MatrixRoom{}).Where("room_id = ?", roomID).Updates(map[string]any{
		"visibility": "public",
		"join_rule":  "public",
	}).Error)
	require.NoError(t, conn.Create(&MatrixGroupHotScore{
		RoomID:     roomID,
		Server:     "remote.example",
		IsLocal:    false,
		ScoreKey:   10,
		ComputedAt: time.Now(),
	}).Error)
	require.NoError(t, conn.Create(&MatrixRoomMember{
		RoomID:     roomID,
		UserID:     remoteMember,
		Membership: MembershipJoin,
		EventID:    "$join_carol",
	}).Error)

	exploreURL := "/api/v1/explore/groups/" + webGroupID(roomID) + "/avatar"
	groupAvatarURL := "/api/v1/chat/groups/" + webGroupID(roomID) + "/avatar"
	memberAvatarURL := "/api/v1/chat/groups/" + webGroupID(roomID) + "/members/" + base64.RawURLEncoding.EncodeToString([]byte(remoteMember)) + "/avatar"

	setMXC := func(suffix string) {
		groupMXC := "mxc://remote.example/group_" + suffix
		memberMXC := "mxc://remote.example/member_" + suffix
		_, e := hs.Event.SendRoomEvent(roomID, "@alice:test.local", "m.room.avatar", nil, map[string]any{"url": groupMXC}, &emptyKey)
		require.Nil(t, e)
		require.NoError(t, conn.Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, EventTypeRoomMember, remoteMember).Delete(&MatrixRoomState{}).Error)
		require.NoError(t, conn.Create(&MatrixRoomState{
			RoomID:    roomID,
			EventType: EventTypeRoomMember,
			StateKey:  remoteMember,
			EventID:   "$state_" + suffix,
			Content:   map[string]any{"membership": "join", "avatar_url": memberMXC},
		}).Error)
	}

	t.Run("upstream_timeout_is_bounded_and_returns_placeholder", func(t *testing.T) {
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		installFakeRemoteMediaFetcher(t, hs, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		setMXC("timeout")

		for _, endpoint := range []string{exploreURL, groupAvatarURL, memberAvatarURL} {
			start := time.Now()
			resp := webGroupFixtureDownload(router, endpoint, "@alice:test.local")
			elapsed := time.Since(start)
			require.Less(t, elapsed, 1500*time.Millisecond, "avatar endpoint %s must have bounded latency", endpoint)
			assertPlaceholderPNG(t, resp.Code, resp.Header(), resp.Body.Bytes())
		}
	})

	t.Run("upstream_5xx_returns_placeholder_instead_of_502", func(t *testing.T) {
		installFakeRemoteMediaFetcher(t, hs, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		setMXC("5xx")

		for _, endpoint := range []string{exploreURL, groupAvatarURL, memberAvatarURL} {
			resp := webGroupFixtureDownload(router, endpoint, "@alice:test.local")
			assertPlaceholderPNG(t, resp.Code, resp.Header(), resp.Body.Bytes())
		}
	})

	t.Run("upstream_404_returns_placeholder_instead_of_502", func(t *testing.T) {
		installFakeRemoteMediaFetcher(t, hs, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		setMXC("404")

		for _, endpoint := range []string{exploreURL, groupAvatarURL, memberAvatarURL} {
			resp := webGroupFixtureDownload(router, endpoint, "@alice:test.local")
			assertPlaceholderPNG(t, resp.Code, resp.Header(), resp.Body.Bytes())
		}
	})

	t.Run("healthy_remote_avatar_streams_real_image", func(t *testing.T) {
		var validPNG bytes.Buffer
		require.NoError(t, png.Encode(&validPNG, image.NewNRGBA(image.Rect(0, 0, 16, 16))))
		installFakeRemoteMediaFetcher(t, hs, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			multipartMedia(w, "Content-Type: image/png\r\nContent-Disposition: inline; filename=\"avatar.png\"\r\n\r\n"+validPNG.String())
		}))
		setMXC("ok")

		for _, endpoint := range []string{exploreURL, groupAvatarURL, memberAvatarURL} {
			resp := webGroupFixtureDownload(router, endpoint, "@alice:test.local")
			require.Equal(t, http.StatusOK, resp.Code)
			require.Empty(t, resp.Header().Get("X-Avatar-Fallback"))
			require.Equal(t, "image/png", resp.Header().Get("Content-Type"))
		}
	})
}
