package admin

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/config"
	emailsmtp "github.com/kasuganosora/community/email/smtp"
	"github.com/kasuganosora/community/runtimeconfig"
	"github.com/kasuganosora/community/storage"
)

// TestEmailSettings checks saved effective settings; it never accepts a URL or
// credentials in the request, sends no message, and never exposes server errors.
func TestEmailSettings(c *gin.Context) {
	cfg := runtimeconfig.EffectiveConfig()
	if cfg.EmailMode != "smtp" || strings.TrimSpace(cfg.SmtpServer) == "" {
		c.JSON(422, gin.H{"success": false, "message": "Save the SMTP server configuration first"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := checkSMTP(ctx, cfg); err != nil {
		c.JSON(422, gin.H{"success": false, "message": "SMTP connection, certificate or authentication check failed; check the saved configuration and network. No email was sent."})
		return
	}
	c.JSON(200, gin.H{"success": true, "message": "SMTP connection and authentication check passed; no email was sent. Saved settings are already used for sending."})
}
func checkSMTP(ctx context.Context, cfg config.Config) error {
	port := cfg.SmtpPort
	if port == "" {
		port = "587"
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("invalid SMTP port")
	}
	host := strings.TrimSpace(cfg.SmtpServer)
	if strings.ContainsAny(host, "/@?#\\\r\n") {
		return errors.New("invalid SMTP host")
	}
	address := net.JoinHostPort(host, port)
	var conn net.Conn
	if config.IsExplicitlyConfigured("SMTP_SERVER") {
		conn, err = (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = activitypub.DialPublicEndpoint(ctx, "tcp", address)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	deadline := time.Now().Add(10 * time.Second)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	_ = conn.SetDeadline(deadline)
	encrypted := false
	if emailsmtp.ImplicitTLS(cfg.SmtpTLS) {
		secured := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		if err = secured.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = secured
		encrypted = true
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()
	if !encrypted {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err = client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return err
			}
			encrypted = true
		}
	}
	if !encrypted && !emailsmtp.LoopbackHost(host) {
		return errors.New("TLS required")
	}
	if cfg.SmtpUsername != "" {
		auth := emailsmtp.Authentication(cfg.SmtpAuthMethod, cfg.SmtpUsername, cfg.SmtpPassword, host)
		if err = client.Auth(auth); err != nil {
			return err
		}
	}
	return client.Noop()
}

type diagnosticTransport struct{ next http.RoundTripper }

func (t diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := t.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		res.Body.Close()
		return nil, errors.New("redirects disabled")
	}
	return res, nil
}

// TestStorageSettings only checks access to an existing bucket: no creation,
// object upload/deletion or public bucket-policy changes are performed.
func TestStorageSettings(c *gin.Context) {
	cfg := runtimeconfig.EffectiveConfig()
	if cfg.StorageType != "s3" && cfg.StorageType != "minio" {
		c.JSON(200, gin.H{"success": true, "message": "Local storage is selected; no object storage connection is needed."})
		return
	}
	endpoint := cfg.Other["S3_ENDPOINT"]
	bucket := cfg.Other["S3_BUCKET"]
	if bucket == "" {
		bucket = "community"
	}
	if endpoint == "" || cfg.Other["S3_ACCESS_KEY"] == "" || cfg.Other["S3_SECRET_KEY"] == "" {
		c.JSON(422, gin.H{"success": false, "message": "Save the endpoint, access key and secret key first"})
		return
	}
	var transport http.RoundTripper = activitypub.NewAuthenticatedMediaHTTPClient(nil).Transport
	if config.IsExplicitlyConfigured("S3_ENDPOINT") {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = nil
		transport = t
	}
	// Same client construction as the running backend (scheme in endpoint,
	// region, path style), probed with a HeadBucket request.
	backend := storage.NewS3Backend(storage.S3Config{
		Endpoint: endpoint, Region: cfg.Other["S3_REGION"], Bucket: bucket,
		AccessKey: cfg.Other["S3_ACCESS_KEY"], SecretKey: cfg.Other["S3_SECRET_KEY"],
		UseSSL: cfg.Other["S3_USE_SSL"] == "true", PathStyle: strings.ToLower(cfg.Other["S3_FORCE_PATH_STYLE"]),
		Transport: diagnosticTransport{next: transport},
	})
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := backend.Check(ctx); err != nil {
		c.JSON(422, gin.H{"success": false, "message": "Cannot access the configured bucket; check its name, permissions, TLS and network. No bucket was created or changed."})
		return
	}
	c.JSON(200, gin.H{"success": true, "message": "Bucket access check passed; no files were uploaded and no access policy was changed. The new configuration takes effect after a restart."})
}
