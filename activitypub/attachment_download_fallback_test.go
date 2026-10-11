package activitypub

import (
	"github.com/kasuganosora/community/models"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestUnknownAttachmentNoteHasSafeDownloadFallback(t *testing.T) {
	a := &models.Account{Id: 1, Username: "alice"}
	to := models.Account{Id: 2, Username: "bob"}
	s := &models.Status{Id: 3, Account: a, AccountId: 1, Visibility: models.StatusVisibilityDirect, ToAccounts: models.Accounts{&to}, Content: models.StatusContent{Content: "File"}, Attachments: models.Attachments{&models.MediaAttachment{Id: 4, FileName: "opaque-random.txt", Description: `report <img src=x onerror=bad()> & notes`, ContentType: "text/plain"}}}
	n := buildNoteFromStatus(s, a, "https://local.example")
	body := n["content"].(string)
	require.Contains(t, body, `href="https://local.example/media/4/opaque-random.txt"`)
	require.Contains(t, body, "&lt;img")
	require.NotContains(t, body, "<img")
	require.NotContains(t, n["to"], publicAddress)
	require.NotContains(t, n["cc"], publicAddress)
	s.Attachments[0].RemoteUrl = "javascript:alert(1)"
	n = buildNoteFromStatus(s, a, "https://local.example")
	require.NotContains(t, n["content"], "javascript:")
	s.Attachments[0].RemoteUrl = ""
	s.Attachments[0].ContentType = "image/png"
	n = buildNoteFromStatus(s, a, "https://local.example")
	require.NotContains(t, n["content"], "/media/4/")
}
