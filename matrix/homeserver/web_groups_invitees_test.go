package homeserver

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func webInviteeAccount(t *testing.T, conn *gorm.DB, id uint64, name, display string) {
	t.Helper()
	require.NoError(t, conn.Create(&models.User{Id: id, UserName: name, EmailHash: name}).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Extra").Create(&models.Account{Id: id, UserId: id, Username: name, DisplayName: display}).Error)
}

func webInviteeSearch(t *testing.T, r *gin.Engine, q string) []any {
	t.Helper()
	status, out := webGroupTestRequest(t, r, "GET", "/chat/groups/invitees?q="+url.QueryEscape(q), "", "")
	require.Equal(t, 200, status, out)
	return out["users"].([]any)
}

func TestWebGroupInviteesOnlyActiveLocalCommunityAccounts(t *testing.T) {
	_, conn, r := setupWebGroupCore(t)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 2).UpdateColumn("display_name", "邀请本人").Error)
	for id := uint64(10); id <= 17; id++ {
		webInviteeAccount(t, conn, id, fmt.Sprintf("invite%d", id), "邀请候选")
	}
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 11).UpdateColumn("is_ban", true).Error)
	require.NoError(t, conn.Model(&models.User{}).Where("id = ?", 12).UpdateColumn("is_ban", true).Error)
	require.NoError(t, conn.Model(&models.User{}).Where("id = ?", 13).UpdateColumn("status", models.UserStatusUnapproved).Error)
	require.NoError(t, conn.Delete(&models.Account{}, 14).Error)
	require.NoError(t, conn.Delete(&models.User{}, 15).Error)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 16).UpdateColumn("domain", "remote.example").Error)
	require.NoError(t, conn.Model(&models.Account{}).Where("id = ?", 17).UpdateColumn("user_id", 99999).Error)
	require.NoError(t, conn.Session(&gorm.Session{SkipHooks: true}).Omit("Extra").Create(&models.Account{Id: 18, Username: "ghost_ap", DisplayName: "邀请无用户"}).Error)
	got := webInviteeSearch(t, r, "邀请")
	require.Len(t, got, 1)
	account := got[0].(map[string]any)
	require.Equal(t, "invite10", account["username"])
	require.Equal(t, "@invite10:test.local", account["user_id"])
	require.Equal(t, "邀请候选", account["display_name"])
	require.NotEmpty(t, account["avatar_url"])
	require.Len(t, account, 4, "suggestions expose only their public display fields")
	for _, user := range []string{"@remote:remote.example", "@ghost_ap:test.local", "@invite11:test.local", "@invite12:test.local", "@invite13:test.local"} {
		status, _ := webGroupTestRequest(t, r, "GET", "/chat/groups/invitees?q=invite", "", user)
		require.Equal(t, 403, status, user)
	}
}

func TestWebGroupInviteesNamesLocalMatrixIDsAndLiteralWildcards(t *testing.T) {
	_, conn, r := setupWebGroupCore(t)
	webInviteeAccount(t, conn, 10, "bobby", "我是小波")
	webInviteeAccount(t, conn, 11, "b", "100%好人")
	webInviteeAccount(t, conn, 12, "qa_one", "测试")
	webInviteeAccount(t, conn, 13, "qaxone", "测试")
	for _, q := range []string{"bob", "@bob", "@BoB:TEST.LOCAL"} {
		got := webInviteeSearch(t, r, q)
		require.Len(t, got, 2, q)
		require.Equal(t, "bob", got[0].(map[string]any)["username"], q)
		require.Equal(t, "bobby", got[1].(map[string]any)["username"], q)
	}
	require.Equal(t, "b", webInviteeSearch(t, r, "b")[0].(map[string]any)["username"], "an exact username ranks before older prefix matches")
	require.Equal(t, "bobby", webInviteeSearch(t, r, "小波")[0].(map[string]any)["username"])
	for q, want := range map[string]string{"qa_": "qa_one", "%": "b"} {
		got := webInviteeSearch(t, r, q)
		require.Len(t, got, 1)
		require.Equal(t, want, got[0].(map[string]any)["username"])
	}
	for _, q := range []string{"", "@", "  ", "alice", "@bob:elsewhere.example", "@bob@test.local", "https://test.local/@bob", "@bob:tes"} {
		require.Empty(t, webInviteeSearch(t, r, q), q)
	}
}

func TestWebGroupInviteesBoundedInputAndResults(t *testing.T) {
	_, conn, r := setupWebGroupCore(t)
	for id := uint64(20); id < 32; id++ {
		webInviteeAccount(t, conn, id, fmt.Sprintf("result%d", id), "Result")
	}
	require.Len(t, webInviteeSearch(t, r, "result"), 8)
	status, out := webGroupTestRequest(t, r, "GET", "/chat/groups/invitees?q=result&limit=2", "", "")
	require.Equal(t, 200, status)
	require.Len(t, out["users"], 2)
	for _, query := range []string{"q=" + strings.Repeat("a", 256), "q=bad%00name", "q=bob&limit=-1", "q=bob&limit=21", "q=bob&limit=NaN"} {
		status, _ = webGroupTestRequest(t, r, "GET", "/chat/groups/invitees?"+query, "", "")
		require.Equal(t, 400, status, query)
	}
	// A deleted login must not continue to access this authenticated directory.
	require.NoError(t, conn.Model(&models.User{}).Where("id = ?", 2).UpdateColumn("deleted_at", time.Now()).Error)
	status, _ = webGroupTestRequest(t, r, "GET", "/chat/groups/invitees?q=result", "", "")
	require.Equal(t, 403, status)
}
