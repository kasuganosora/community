package homeserver

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type webGroupAttachment struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Size         int64  `json:"size"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`
}

func webGroupMXC(value string) (string, string, error) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "mxc" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Count(u.Path, "/") != 1 {
		return "", "", errors.New("invalid media reference")
	}
	id := strings.TrimPrefix(u.Path, "/")
	if err := validRemoteMediaTarget(u.Host, id); err != nil {
		return "", "", err
	}
	return u.Host, id, nil
}

func webGroupMediaURL(roomID, eventID, mxc string) string {
	if _, _, err := webGroupMXC(mxc); err != nil {
		return ""
	}
	return "/api/v1/chat/groups/" + webGroupID(roomID) + "/messages/" + url.PathEscape(eventID) + "/media/" + base64.RawURLEncoding.EncodeToString([]byte(mxc))
}

func webGroupAttachments(roomID string, event MatrixEvent) []webGroupAttachment {
	if event.RoomID != roomID {
		return nil
	}
	raw, _ := event.Content["url"].(string)
	mediaURL := webGroupMediaURL(roomID, event.EventID, raw)
	if mediaURL == "" {
		return nil
	}
	name, _ := event.Content["filename"].(string)
	if name == "" {
		name, _ = event.Content["body"].(string)
	}
	info, _ := event.Content["info"].(map[string]any)
	typ, _ := info["mimetype"].(string)
	if typ == "" {
		typ = "application/octet-stream"
	}
	attachment := webGroupAttachment{ID: base64.RawURLEncoding.EncodeToString([]byte(raw)), Name: webGroupFilename(name), Type: typ,
		Size: webGroupMediaSize(info["size"]), URL: mediaURL}
	if webGroupRasterType(typ) && event.Content["msgtype"] == "m.image" {
		attachment.ThumbnailURL = mediaURL + "?thumbnail=1"
	}
	return []webGroupAttachment{attachment}
}

func webGroupMediaSize(value any) int64 {
	var size int64
	switch n := value.(type) {
	case float64:
		if n > 0 && n < 1<<53 {
			size = int64(n)
		}
	case int64:
		size = n
	case int:
		size = int64(n)
	}
	return max(size, 0)
}

func handleWebGroupMedia(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, false)
	if !ok {
		return
	}
	if len(c.Param("mediaID")) > 1024 || len(c.Param("messageID")) > 255 {
		webGroupFailure(c, 400, "INVALID_MEDIA", "Invalid attachment address")
		return
	}
	decoded, err := base64.RawURLEncoding.DecodeString(c.Param("mediaID"))
	if err != nil {
		webGroupFailure(c, 400, "INVALID_MEDIA", "Invalid attachment address")
		return
	}
	mxc := string(decoded)
	conn := db.GetDB().WithContext(c.Request.Context())
	var event MatrixEvent
	if err := conn.Where("room_id = ? AND event_id = ?", room.RoomID, c.Param("messageID")).First(&event).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			webGroupFailure(c, 503, "MEDIA_UNAVAILABLE", "Cannot check attachment permissions right now")
			return
		}
		webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "The attachment does not exist or you may not access it")
		return
	}
	reader, err := newClientHistoryReader(conn, room.RoomID, user)
	if err != nil {
		webGroupFailure(c, 503, "MEDIA_UNAVAILABLE", "Cannot check attachment permissions right now")
		return
	}
	visible, err := reader.filter([]MatrixEvent{event})
	if err != nil {
		webGroupFailure(c, 503, "MEDIA_UNAVAILABLE", "Cannot check attachment permissions right now")
		return
	}
	if len(visible) == 0 {
		webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "The attachment does not exist or you may not access it")
		return
	}
	// The URL belongs to this exact visible, non-redacted event. Being logged
	// in or knowing another room's MXC never grants access through this route.
	content := event.ToClientEvent().Content
	relation, _ := content["m.relates_to"].(map[string]any)
	isEdit := relation["rel_type"] == "m.replace" && event.EventType == EventTypeRoomMessage
	isReaction := relation["rel_type"] == "m.annotation" && event.EventType == "m.reaction"
	if isEdit || isReaction {
		target, _ := relation["event_id"].(string)
		var original MatrixEvent
		query := conn.Where("room_id = ? AND event_id = ? AND event_type = ? AND state_key IS NULL", room.RoomID, target, EventTypeRoomMessage)
		if isEdit {
			query = query.Where("sender = ?", event.Sender)
		}
		err := query.First(&original).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			webGroupFailure(c, 503, "MEDIA_UNAVAILABLE", "Cannot check attachment permissions right now")
			return
		}
		if err != nil || len(original.ToClientEvent().Content) == 0 {
			webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "The attachment does not exist or you may not access it")
			return
		}
		visibleOriginal, err := reader.filter([]MatrixEvent{original})
		if err != nil {
			webGroupFailure(c, 503, "MEDIA_UNAVAILABLE", "Cannot check attachment permissions right now")
			return
		}
		if len(visibleOriginal) == 0 {
			webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "The attachment does not exist or you may not access it")
			return
		}
		if isEdit {
			content, _ = content["m.new_content"].(map[string]any)
		}
	}
	event.Content = content
	if !webGroupEventReferencesMXC(room.RoomID, event, mxc) || (event.EventType != EventTypeRoomMessage && event.EventType != EventTypeRoomAvatar && event.EventType != "m.sticker" && !isReaction) {
		webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "The attachment does not exist or you may not access it")
		return
	}
	name, _ := content["filename"].(string)
	if name == "" {
		name, _ = content["body"].(string)
	}
	serveWebGroupMXC(c, hs, mxc, name, c.Query("thumbnail") == "1")
}

// A current room avatar is state visible to members/invitees, not historical
// message content; joined-history rooms must still show an older room avatar.
func handleWebGroupAvatar(c *gin.Context) {
	hs, room, user, ok := webGroupAccess(c, false)
	if !ok {
		return
	}
	var member MatrixRoomMember
	if db.GetDB().WithContext(c.Request.Context()).Where("room_id = ? AND user_id = ? AND membership IN ?", room.RoomID, user, []MembershipState{MembershipJoin, MembershipInvite}).First(&member).Error != nil || room.AvatarURL == nil {
		webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "The group avatar does not exist or you may not access it")
		return
	}
	serveWebGroupMXC(c, hs, *room.AvatarURL, "avatar", true)
}

func handleWebGroupMemberAvatar(c *gin.Context) {
	hs, room, _, ok := webGroupAccess(c, true)
	if !ok {
		return
	}
	if len(c.Param("memberID")) > 512 {
		webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "Member avatar not found")
		return
	}
	decoded, err := base64.RawURLEncoding.DecodeString(c.Param("memberID"))
	if err != nil {
		webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "Member avatar not found")
		return
	}
	var member MatrixRoomMember
	conn := db.GetDB().WithContext(c.Request.Context())
	if err := conn.Where("room_id = ? AND user_id = ?", room.RoomID, string(decoded)).First(&member).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			webGroupFailure(c, 503, "MEDIA_UNAVAILABLE", "Cannot load the member avatar right now")
			return
		}
		webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "Member avatar not found")
		return
	}
	avatar := ""
	if member.AvatarURL != nil {
		avatar = *member.AvatarURL
	}
	var state MatrixRoomState
	if err := conn.Where("room_id = ? AND event_type = ? AND state_key = ?", room.RoomID, EventTypeRoomMember, string(decoded)).First(&state).Error; err == nil {
		avatar, _ = state.Content["avatar_url"].(string)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		webGroupFailure(c, 503, "MEDIA_UNAVAILABLE", "Cannot load the member avatar right now")
		return
	}
	if _, _, mxcErr := webGroupMXC(avatar); mxcErr != nil {
		local, server, valid := ParseMatrixUserID(string(decoded))
		if valid && server == hs.Config.ServerName {
			var account models.Account
			if err := conn.Where("username = ? AND domain IS NULL AND user_id <> 0", local).First(&account).Error; err == nil {
				avatar = hs.bridgeCompatibleProfileAvatar(c.Request.Context(), string(decoded), account)
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				webGroupFailure(c, 503, "MEDIA_UNAVAILABLE", "Cannot load the member avatar right now")
				return
			}
		}
	}
	if avatar == "" {
		webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "Member avatar not found")
		return
	}
	serveWebGroupMXC(c, hs, avatar, "avatar", true)
}

var webGroupDownloadSlots = make(chan struct{}, 16)

func serveWebGroupMXC(c *gin.Context, hs *HomeServer, mxc, name string, thumbnail bool) {
	server, id, err := webGroupMXC(mxc)
	if err != nil {
		webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "Invalid attachment address")
		return
	}
	select {
	case webGroupDownloadSlots <- struct{}{}:
		defer func() { <-webGroupDownloadSlots }()
	default:
		c.Header("Retry-After", "3")
		webGroupFailure(c, 429, "MEDIA_BUSY", "Attachment downloads are busy, please try again later")
		return
	}
	var body io.ReadCloser
	length := int64(-1)
	if server == hs.Config.ServerName {
		svc := NewMediaService(hs)
		if thumbnail {
			_, body, err = svc.GetThumbnail(server, id, 320, 240, "scale")
		} else {
			var media *MatrixMedia
			media, body, err = svc.getMediaContext(c.Request.Context(), server, id)
			if err == nil {
				length = media.FileSize
				if name == "" {
					name = media.FileName
				}
			}
		}
	} else {
		kind := "download"
		var params url.Values
		if thumbnail {
			kind = "thumbnail"
			params = url.Values{"width": {"320"}, "height": {"240"}, "method": {"scale"}}
		}
		remote, cancel, fetchErr := hs.remoteMedia().Open(c.Request.Context(), server, id, kind, params)
		defer cancel()
		err = fetchErr
		if err == nil {
			body, length = remote.Body, remote.Length
			if name == "" {
				name = remote.Filename
			}
		}
	}
	if err != nil {
		_ = c.Error(fmt.Errorf("web groups media %s: %w", name, err)) // request-id correlated server log
		switch {
		case errors.Is(err, errMediaNotFound):
			webGroupFailure(c, 404, "MEDIA_NOT_FOUND", "The attachment does not exist")
		case server == hs.Config.ServerName:
			// Local storage/database hiccups (e.g. right after a restart) are
			// ours, not an upstream's: tell the client when to retry.
			c.Header("Retry-After", "5")
			webGroupFailure(c, 503, "MEDIA_UNAVAILABLE", "The attachment is temporarily unavailable, please try again later")
		default:
			c.Header("Retry-After", "30")
			webGroupFailure(c, 502, "MEDIA_UNAVAILABLE", "The attachment is temporarily unavailable, please try again later")
		}
		return
	}
	defer body.Close()
	if length > matrixMediaLimit {
		webGroupFailure(c, 413, "MEDIA_TOO_LARGE", "The attachment exceeds the web download size limit; use a client")
		return
	}
	serveWebGroupMediaBytes(c, body, length, name, thumbnail)
}

func serveWebGroupMediaBytes(c *gin.Context, body io.Reader, length int64, name string, thumbnail bool) {
	// Bounded sniffing rather than trusting the filename or remote MIME header.
	br := bufio.NewReaderSize(io.LimitReader(body, matrixMediaLimit), 64<<10)
	prefix, _ := br.Peek(512)
	typ := http.DetectContentType(prefix)
	disposition := "attachment"
	if webGroupRasterType(typ) {
		imageHeader, _ := br.Peek(64 << 10)
		cfg, _, err := image.DecodeConfig(bytes.NewReader(imageHeader))
		if err == nil && cfg.Width > 0 && cfg.Height > 0 && int64(cfg.Width)*int64(cfg.Height) <= webGroupImageMaxPixels {
			if c.Query("download") != "1" {
				disposition = "inline"
			}
		} else {
			typ = "application/octet-stream"
		}
	}
	if disposition != "inline" {
		// Includes SVG/HTML, audio/video and oversized/invalid raster images.
		typ = "application/octet-stream"
	}
	if thumbnail && disposition != "inline" && c.Query("download") != "1" {
		webGroupFailure(c, 415, "PREVIEW_UNAVAILABLE", "This file cannot be previewed; download it to view")
		return
	}
	c.Header("Content-Type", typ)
	c.Header("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": webGroupFilename(name)}))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "sandbox; default-src 'none'")
	c.Header("Cross-Origin-Resource-Policy", "same-origin")
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Authorization")
	if length >= 0 {
		c.Header("Content-Length", strconv.FormatInt(length, 10))
	}
	c.Status(http.StatusOK)
	if _, err := io.Copy(c.Writer, br); err != nil {
		log.Warn("web group media stream interrupted: %v", err)
	}
}
