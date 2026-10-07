package smtp

import (
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/email/types"
	"github.com/stretchr/testify/require"
	"net/mail"
	netsmtp "net/smtp"
	"strings"
	"testing"
)

func TestLoginAuthenticationTLSHostAndChallengeSequence(t *testing.T) {
	auth := Authentication(" LOGIN ", "operator", "password", "smtp.example.com")
	for _, info := range []*netsmtp.ServerInfo{nil, {Name: "smtp.example.com", TLS: false}, {Name: "attacker.example", TLS: true}} {
		_, initial, err := auth.Start(info)
		require.Error(t, err)
		require.Empty(t, initial)
	}
	protocol, initial, err := auth.Start(&netsmtp.ServerInfo{Name: "smtp.example.com", TLS: true})
	require.NoError(t, err)
	require.Equal(t, "LOGIN", protocol)
	require.Empty(t, initial)
	user, err := auth.Next([]byte("Username:"), true)
	require.NoError(t, err)
	require.Equal(t, "operator", string(user))
	password, err := auth.Next([]byte("Password:"), true)
	require.NoError(t, err)
	require.Equal(t, "password", string(password))
	_, err = auth.Next([]byte("Another:"), true)
	require.Error(t, err)
	_, _, err = auth.Start(&netsmtp.ServerInfo{Name: "smtp.example.com", TLS: true})
	require.NoError(t, err)
	user, err = auth.Next([]byte("Username:"), true)
	require.NoError(t, err)
	require.Equal(t, "operator", string(user))
	_, err = auth.Next(nil, false)
	require.NoError(t, err)
}
func TestAuthenticationModesNeverDowngradeUnknownOrRemotePlaintext(t *testing.T) {
	for _, method := range []string{"PLAIN", "LOGIN", "CRAM-MD5", "unsupported"} {
		_, data, err := Authentication(method, "u", "p", "smtp.example.com").Start(&netsmtp.ServerInfo{Name: "smtp.example.com", TLS: false})
		require.Error(t, err)
		require.Empty(t, data)
	}
	_, _, err := Authentication("unsupported", "u", "p", "smtp.example.com").Start(&netsmtp.ServerInfo{Name: "smtp.example.com", TLS: true})
	require.Error(t, err)
	_, _, err = Authentication("LOGIN", "u", "p", "127.0.0.1").Start(&netsmtp.ServerInfo{Name: "127.0.0.1"})
	require.NoError(t, err)
	require.False(t, ImplicitTLS(" FALSE "))
	require.False(t, ImplicitTLS(" 0 "))
	require.True(t, ImplicitTLS(""))
}
func TestSMTPFromIsSameInHeaderAndEnvelope(t *testing.T) {
	withConfig(t, "127.0.0.1", "0", "login-account", "pw", "false", "10")
	config.C.SmtpFrom = "Community News <announcements@example.com>"
	tlsLeafCert = generateLeafCert(t)
	probe := &smtpServerProbe{}
	srv := newMockSMTPServer(t, captureFirst(handlerFullSession, probe))
	defer srv.Close()
	d := newDriveFromServer(srv.Host(), srv.Port(), false)
	require.NoError(t, d.SendMail("to@example.com", "", &types.Mail{Subject: "Sender test", TextBody: "test"}))
	srv.Close()
	session := probe.cloneSession()
	require.Equal(t, "announcements@example.com", session.From)
	msg, err := mail.ReadMessage(strings.NewReader(string(session.Data)))
	require.NoError(t, err)
	sender, err := mail.ParseAddress(msg.Header.Get("From"))
	require.NoError(t, err)
	require.Equal(t, session.From, sender.Address)
	require.Equal(t, "Community News", sender.Name)
	require.Equal(t, "login-account", session.AuthUser)
	require.True(t, session.DidStartTLS)
}
func TestInvalidExplicitSMTPFromRejectedBeforeDial(t *testing.T) {
	withConfig(t, "smtp.invalid", "587", "u", "p", "true", "1")
	config.C.SmtpFrom = "invalid\r\nBcc: outsider@example.com"
	require.ErrorContains(t, NewDrive().Send("to@example.com", "", []byte("message")), "invalid SMTP sender")
}
