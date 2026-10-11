package homeserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime"
	"net/url"
	"strings"
	"time"

	"github.com/kasuganosora/community/storage"
)

// validateBridgeRemoteMXC is admission-only: it never performs network I/O.
// The caller must first verify the event's federation signature, room membership
// and explicit mixed-room consent. An MXC on an unrelated server is not a
// permission to retrieve a third party's media.
func (hs *HomeServer) validateBridgeRemoteMXC(event *MatrixEvent) (string, string, error) {
	if event == nil || event.EventID == "" || event.FederationJSON == "" {
		return "", "", errDirectMessageSkipped
	}
	if _, encrypted := event.Content["file"]; encrypted {
		return "", "", errDirectMessageSkipped
	}
	_, server, ok := strings.Cut(event.Sender, ":")
	if !ok || !strings.HasPrefix(event.Sender, "@") || server == "" || server == hs.Config.ServerName {
		return "", "", errDirectMessageSkipped
	}
	raw, _ := event.Content["url"].(string)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "mxc" || u.Host != server || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return "", "", errDirectMessageSkipped
	}
	id := strings.TrimPrefix(u.Path, "/")
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") || len(id) > 255 {
		return "", "", errDirectMessageSkipped
	}
	// Bind the persisted wire identity to the admitted event. This is not a
	// replacement for signature verification performed by federation ingestion.
	var pdu struct {
		Sender     string                       `json:"sender"`
		Signatures map[string]map[string]string `json:"signatures"`
	}
	if json.Unmarshal([]byte(event.FederationJSON), &pdu) != nil || pdu.Sender != event.Sender || len(pdu.Signatures[server]) == 0 {
		return "", "", errDirectMessageSkipped
	}
	return server, id, nil
}

// importBridgeRemoteMedia is worker-only, after durable admission. Fetch errors
// are returned for persistent retry; they must not reject an already accepted
// Matrix event. The source is never exposed as an unauthenticated remote URL.
func (hs *HomeServer) importBridgeRemoteMedia(ctx context.Context, event *MatrixEvent) (*MatrixMedia, error) {
	server, id, err := hs.validateBridgeRemoteMXC(event)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	remote, err := hs.mediaFederation().fetchAuthenticatedMediaContext(ctx, server, id, "download", "")
	if err != nil {
		return nil, err
	}
	if len(remote.Data) == 0 || int64(len(remote.Data)) > matrixMediaLimit {
		return nil, errors.New("invalid remote bridge media size")
	}
	typ, _, err := mime.ParseMediaType(remote.ContentType)
	if err != nil {
		return nil, errors.New("invalid remote bridge media type")
	}
	// Keep arbitrary files downloadable rather than executing active HTML/SVG.
	if typ == "text/html" || typ == "application/xhtml+xml" || typ == "image/svg+xml" {
		typ = "application/octet-stream"
	}
	name := remote.Filename
	if explicit, ok := event.Content["filename"].(string); ok && strings.TrimSpace(explicit) != "" {
		name = explicit
	}
	if name == "" {
		name, _ = event.Content["body"].(string)
	}
	name = safeBridgeFilename(name)
	digest := sha256.Sum256([]byte(event.EventID + "\x00" + event.Sender + "\x00" + server + "/" + id))
	// This staging object has no public media row/URL. A deterministic key bounds
	// storage on retries; the outbound attachment obtains its own access key.
	key := "matrix-media/bridge-remote/" + hex.EncodeToString(digest[:])
	size := int64(len(remote.Data))
	if err = storage.Default().Put(ctx, key, bytes.NewReader(remote.Data), size, typ); err != nil {
		return nil, err
	}
	return &MatrixMedia{ServerName: hs.Config.ServerName, UploaderID: event.Sender, FilePath: key, FileName: name, ContentType: typ, FileSize: size}, nil
}
