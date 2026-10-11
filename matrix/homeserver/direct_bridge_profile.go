package homeserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/defaultavatar"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

const bridgeAvatarSourceKey = "org.community.avatar_source"
const bridgeAvatarManualKey = "org.community.avatar_manual"
const bridgeAvatarMaxBytes = 2 << 20

// Kept separate from attachment import: only public profile images are accepted.
// The production transport pins DNS and rejects private-address redirects.
var newBridgeAvatarHTTPClient = func() *http.Client {
	client := activitypub.NewAuthenticatedMediaHTTPClient(nil)
	// Public unsigned profile images may redirect to a CDN. Every hop still
	// uses the DNS-pinned transport; never relax TLS or allow HTTP downgrade.
	client.CheckRedirect = bridgeAvatarRedirect
	return client
}

func bridgeAvatarRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("too many avatar redirects")
	}
	if req.URL.Scheme != "https" || req.URL.Hostname() == "" || req.URL.User != nil || req.URL.Fragment != "" {
		return errors.New("unsafe avatar redirect")
	}
	return nil
}

func (hs *HomeServer) syncBridgeProfile(ctx, avatarBudget context.Context, roomID, userID string, account models.Account) error {
	conn := db.GetDB().WithContext(ctx)
	var state MatrixRoomState
	if err := conn.Where("room_id = ? AND event_type = ? AND state_key = ?", roomID, EventTypeRoomMember, userID).First(&state).Error; err != nil {
		return err
	}
	if state.Content["membership"] != "join" {
		return nil
	}
	display := account.DisplayName
	if display == "" {
		display = account.Username
	}
	if account.Domain != nil {
		acct := account.Username + "@" + *account.Domain
		if account.DisplayName != "" {
			display = account.DisplayName + " (" + acct + ")"
		} else {
			display = acct
		}
	}
	source := account.AvatarStatic
	if source == "" || strings.HasPrefix(account.Avatar, "mxc://") {
		source = account.Avatar
	}
	modified := ""
	if account.UpdatedAt != nil {
		modified = account.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s\x00%s", account.Id, source, modified)))
	fingerprint := hex.EncodeToString(sum[:])
	avatar, _ := state.Content["avatar_url"].(string)
	oldFingerprint, _ := state.Content[bridgeAvatarSourceKey].(string)
	nextFingerprint := oldFingerprint
	if account.Domain == nil && (state.Content[bridgeAvatarManualKey] == true || (strings.HasPrefix(avatar, "mxc://") && !strings.HasPrefix(avatar, "mxc://"+hs.Config.ServerName+"/"+defaultavatar.MediaPrefix) && (oldFingerprint == "" || strings.HasPrefix(account.Avatar, "mxc://")))) {
		// A user's explicit Matrix avatar remains authoritative over Community defaults.
		nextFingerprint = oldFingerprint
	} else if defaultavatar.IsPlaceholder(source, systemsettings.GetBaseURL()) {
		avatar = hs.defaultBridgeAvatar(account)
		nextFingerprint = fingerprint
	} else if oldFingerprint != fingerprint || !strings.HasPrefix(avatar, "mxc://") {
		avatarCtx, cancel := context.WithTimeout(avatarBudget, 3*time.Second)
		imported, err := hs.importBridgeAvatar(avatarCtx, userID, source, account.Domain == nil)
		cancel()
		if err == nil {
			avatar = imported
			nextFingerprint = fingerprint
		}
		// An unavailable avatar never delays/rejects the private message. Keep the
		// prior valid MXC, without marking a failed fetch as synchronized.
	}
	if !validBridgeMXC(avatar) {
		avatar = hs.defaultBridgeAvatar(account)
	}
	persistProfile := func() error {
		return conn.Model(&MatrixRoomMember{}).Where("room_id = ? AND user_id = ? AND membership = ?", roomID, userID, MembershipJoin).Updates(map[string]any{"display_name": display, "avatar_url": avatar}).Error
	}
	if state.Content["displayname"] == display && state.Content["avatar_url"] == avatar && oldFingerprint == nextFingerprint {
		return persistProfile()
	}
	content := map[string]any{}
	for key, value := range state.Content {
		content[key] = value
	}
	content["membership"] = "join"
	content["displayname"] = display
	content["avatar_url"] = avatar
	if nextFingerprint != "" {
		content[bridgeAvatarSourceKey] = nextFingerprint
	}
	if _, me := hs.Event.SendRoomEvent(roomID, userID, string(EventTypeRoomMember), nil, content, &userID); me != nil {
		return errors.New(me.Error)
	}
	return persistProfile()
}

func (hs *HomeServer) importBridgeAvatar(ctx context.Context, userID, source string, local bool) (string, error) {
	conn := db.GetDB().WithContext(ctx)
	if strings.HasPrefix(source, "mxc://") {
		if !local {
			return "", errors.New("remote avatar must be an HTTPS profile image")
		}
		uri, err := url.Parse(source)
		if err != nil || uri.Host != hs.Config.ServerName || uri.RawQuery != "" || uri.Fragment != "" {
			return "", errors.New("unsupported avatar MXC")
		}
		var media MatrixMedia
		err = conn.Where("server_name = ? AND media_id = ? AND uploader_id = ?", uri.Host, strings.TrimPrefix(uri.Path, "/"), userID).First(&media).Error
		if err != nil || !strings.HasPrefix(media.ContentType, "image/") {
			return "", errors.New("avatar MXC unavailable")
		}
		return source, nil
	}
	var reader io.ReadCloser
	if local && strings.HasPrefix(source, "/avatars/") {
		if path.Clean(source) != source || strings.Contains(source, "\\") {
			return "", errors.New("invalid local avatar key")
		}
		var err error
		reader, _, err = storage.Open(ctx, strings.TrimPrefix(source, "/"))
		if err != nil {
			return "", err
		}
	} else {
		uri, err := url.Parse(source)
		if err != nil || uri.Scheme != "https" || uri.Hostname() == "" || uri.User != nil || uri.Fragment != "" {
			return "", errors.New("invalid avatar URL")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "image/png,image/jpeg,image/gif,image/webp")
		response, err := newBridgeAvatarHTTPClient().Do(req)
		if err != nil {
			return "", err
		}
		if response.StatusCode != http.StatusOK || response.ContentLength > bridgeAvatarMaxBytes {
			response.Body.Close()
			return "", errors.New("avatar unavailable or too large")
		}
		reader = response.Body
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, bridgeAvatarMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > bridgeAvatarMaxBytes {
		return "", errors.New("avatar too large")
	}
	dimensions, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || dimensions.Width < 1 || dimensions.Height < 1 || dimensions.Width > 4096 || dimensions.Height > 4096 {
		return "", errors.New("unsupported avatar image")
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	var normalized bytes.Buffer
	if err = png.Encode(&normalized, decoded); err != nil {
		return "", err
	}
	if normalized.Len() > bridgeAvatarMaxBytes {
		return "", errors.New("normalized avatar too large")
	}
	// Account identity participates in deduplication; importing a public avatar
	// never grants access to a different user's private Matrix attachment row.
	digest := sha256.Sum256(append([]byte(userID+"\x00"), normalized.Bytes()...))
	key := "matrix/bridge-avatars/" + hex.EncodeToString(digest[:]) + ".png"
	var existing MatrixMedia
	err = conn.Where("server_name = ? AND uploader_id = ? AND file_path = ?", hs.Config.ServerName, userID, key).First(&existing).Error
	if err == nil {
		return "mxc://" + hs.Config.ServerName + "/" + existing.MediaID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	if err = storage.Default().Put(ctx, key, bytes.NewReader(normalized.Bytes()), int64(normalized.Len()), "image/png"); err != nil {
		return "", err
	}
	media := MatrixMedia{MediaID: generateMediaID(), ServerName: hs.Config.ServerName, UploaderID: userID, FilePath: key, FileName: "avatar.png", ContentType: "image/png", FileSize: int64(normalized.Len()), Width: &dimensions.Width, Height: &dimensions.Height}
	if err = conn.Create(&media).Error; err != nil {
		return "", err
	}
	return "mxc://" + hs.Config.ServerName + "/" + media.MediaID, nil
}

// Ghost profile data is the already-published member profile, never a login
// account or a private room identifier. The reserved 64-hex localpart cannot
// collide with an ordinary registered Community username.
func (hs *HomeServer) bridgeGhostProfile(ctx context.Context, userID string) (map[string]any, bool) {
	local, server, ok := ParseMatrixUserID(userID)
	if !ok || server != hs.Config.ServerName || !strings.HasPrefix(local, "_ap_") || len(local) != 68 {
		return nil, false
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(local, "_ap_")); err != nil {
		return nil, false
	}
	var member MatrixRoomMember
	if err := db.GetDB().WithContext(ctx).Where("user_id = ? AND membership = ?", userID, MembershipJoin).Order("updated_at DESC, id DESC").First(&member).Error; err != nil {
		return nil, false
	}
	profile := map[string]any{"displayname": "", "avatar_url": ""}
	if member.DisplayName != nil {
		profile["displayname"] = *member.DisplayName
	}
	if member.AvatarURL != nil && validBridgeMXC(*member.AvatarURL) {
		profile["avatar_url"] = *member.AvatarURL
	}
	return profile, true
}

func (hs *HomeServer) bridgeCompatibleProfileAvatar(ctx context.Context, userID string, account models.Account) string {
	source := account.Avatar
	if defaultavatar.IsPlaceholder(source, systemsettings.GetBaseURL()) {
		return hs.defaultBridgeAvatar(account)
	}
	if validBridgeMXC(source) {
		return source
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	avatar, err := hs.importBridgeAvatar(ctx, userID, source, true)
	if err != nil {
		return hs.defaultBridgeAvatar(account)
	}
	return avatar
}

func validBridgeMXC(raw string) bool {
	uri, err := url.Parse(raw)
	return err == nil && uri.Scheme == "mxc" && uri.Host != "" && uri.User == nil && uri.RawQuery == "" && uri.Fragment == "" && len(uri.Path) > 1 && !strings.Contains(strings.TrimPrefix(uri.Path, "/"), "/")
}

func (hs *HomeServer) defaultBridgeAvatar(account models.Account) string {
	domain := systemsettings.GetDomain()
	if account.Domain != nil && *account.Domain != "" {
		domain = *account.Domain
	}
	return "mxc://" + hs.Config.ServerName + "/" + defaultavatar.MediaPrefix + defaultavatar.Seed(account.Username, domain)
}
