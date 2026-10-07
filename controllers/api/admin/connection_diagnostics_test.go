package admin

import (
	"bufio"
	"context"
	"github.com/kasuganosora/community/config"
	"github.com/stretchr/testify/require"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSMTPDiagnosticLoopbackNoMessageAndTLSNormalization(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	t.Setenv("SMTP_SERVER", host)
	done := make(chan []string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- nil
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		writer := bufio.NewWriter(conn)
		reader := bufio.NewReader(conn)
		_, _ = writer.WriteString("220 local test\r\n")
		_ = writer.Flush()
		commands := []string{}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			commands = append(commands, line)
			if strings.HasPrefix(line, "EHLO") {
				_, _ = writer.WriteString("250 local test\r\n")
			} else if strings.HasPrefix(line, "NOOP") {
				_, _ = writer.WriteString("250 OK\r\n")
			} else {
				_, _ = writer.WriteString("500 Unexpected command\r\n")
			}
			_ = writer.Flush()
		}
		done <- commands
	}()
	require.NoError(t, checkSMTP(context.Background(), config.Config{SmtpServer: host, SmtpPort: port, SmtpTLS: " FALSE "}))
	commands := <-done
	require.Len(t, commands, 2)
	require.True(t, strings.HasPrefix(commands[0], "EHLO "))
	require.Equal(t, "NOOP\r\n", commands[1])
}
func TestSMTPDiagnosticRejectsUntrustedTLSAndInvalidPorts(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("untrusted certificate must not reach application handler")
	}))
	defer server.Close()
	host, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "https://"))
	require.NoError(t, err)
	t.Setenv("SMTP_SERVER", host)
	require.Error(t, checkSMTP(context.Background(), config.Config{SmtpServer: host, SmtpPort: port, SmtpTLS: "true"}))
	for _, port := range []string{"0", "65536", "invalid"} {
		require.ErrorContains(t, checkSMTP(context.Background(), config.Config{SmtpServer: host, SmtpPort: port}), "invalid SMTP port")
	}
}
func TestSMTPDiagnosticCancellationInterruptsServerGreeting(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	host, port, err := net.SplitHostPort(listener.Addr().String())
	require.NoError(t, err)
	t.Setenv("SMTP_SERVER", host)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		var b [1]byte
		_, _ = conn.Read(b[:])
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	require.Error(t, checkSMTP(ctx, config.Config{SmtpServer: host, SmtpPort: port, SmtpTLS: "false"}))
	require.Less(t, time.Since(start), time.Second)
	<-done
}
