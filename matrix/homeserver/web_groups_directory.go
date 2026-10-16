package homeserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

func registerWebGroupSearchRoutes(group *gin.RouterGroup) {
	group.GET("/groups/search", handleWebGroupSearch)
	group.GET("/groups/directory-avatar", handleWebGroupDirectoryAvatar)
	group.POST("/groups/resolve", handleWebGroupResolve)
	group.POST("/groups/join", handleWebGroupJoin)
	group.GET("/groups/join/:operationID", handleWebGroupJoinStatus)
}

func validGroupRoomID(id string) bool {
	return strings.HasPrefix(id, "!") && len(id) > 1 && len(id) <= 255 && !strings.ContainsAny(id, "/?#\\") && !strings.ContainsFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}
func validGroupAlias(alias string) bool {
	if !strings.HasPrefix(alias, "#") || len(alias) > 255 || strings.ContainsAny(alias, "/?\\") || strings.ContainsFunc(alias, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
		return false
	}
	i := strings.IndexByte(alias, ':')
	return i > 1 && usableServerName(alias[i+1:])
}

// Parsing is deliberately independent of networking: opening a link cannot join
// a room, and event/user/space links are never silently converted to group joins.
func parseWebGroupTarget(raw string, via []string) (string, []string, error) {
	target := strings.TrimSpace(raw)
	if len(target) > 2048 || len(via) > 20 {
		return "", nil, errors.New("The group address is too long")
	}
	if strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "http://") {
		u, e := url.Parse(target)
		if e != nil || u.Scheme != "https" || u.Host != "matrix.to" || u.User != nil || u.Path != "/" && u.Path != "" {
			return "", nil, errors.New("Enter a Matrix group link or full group alias")
		}
		fragment := strings.TrimPrefix(u.Fragment, "/")
		parts := strings.SplitN(fragment, "?", 2)
		target = parts[0]
		if len(parts) == 2 {
			values, e := url.ParseQuery(parts[1])
			if e != nil {
				return "", nil, e
			}
			via = append(via, values["via"]...)
		}
	} else if strings.HasPrefix(target, "matrix:") {
		u, e := url.Parse(target)
		if e != nil || u.Host != "" {
			return "", nil, errors.New("Invalid group link")
		}
		opaque := u.Opaque
		if opaque == "" {
			opaque = strings.TrimPrefix(u.Path, "/")
		}
		kind, value, ok := strings.Cut(opaque, "/")
		if !ok || strings.Contains(value, "/") {
			return "", nil, errors.New("Only group links are supported, not message or user links")
		}
		value, e = url.PathUnescape(value)
		if e != nil {
			return "", nil, e
		}
		switch kind {
		case "r":
			target = "#" + value
		case "roomid":
			target = "!" + value
		default:
			return "", nil, errors.New("Only group links are supported")
		}
		via = append(via, u.Query()["via"]...)
	}
	if !validGroupRoomID(target) && !validGroupAlias(target) {
		if id, e := webGroupRoomID(target); e == nil && validGroupRoomID(id) {
			target = id
		} else {
			return "", nil, errors.New("Enter a full group alias or Matrix group link")
		}
	}
	if len(via) > 20 {
		return "", nil, errors.New("The group link contains too many servers")
	}
	seen := map[string]bool{}
	clean := []string{}
	for _, s := range via {
		s = strings.TrimSpace(s)
		if !usableServerName(s) {
			return "", nil, errors.New("The group link contains an invalid server")
		}
		if !seen[s] {
			clean = append(clean, s)
			seen[s] = true
		}
	}
	return target, clean, nil
}

func resolveWebGroupTarget(ctx context.Context, hs *HomeServer, target string, via []string) (string, []string, *MatrixError) {
	if strings.HasPrefix(target, "#") {
		result, err := NewAliasService(hs).ResolveAliasContext(ctx, target)
		if err != nil {
			return "", nil, err
		}
		if !validGroupRoomID(result.RoomID) {
			return "", nil, &MatrixError{ErrCode: "M_BAD_GATEWAY", Error: "The group directory returned an invalid group address"}
		}
		seen := map[string]bool{}
		out := []string{}
		for _, s := range append(via, result.Servers...) {
			if usableServerName(s) && !seen[s] {
				seen[s] = true
				out = append(out, s)
				if len(out) >= 20 {
					break
				}
			}
		}
		return result.RoomID, out, nil
	}
	return target, via, nil
}

func directoryWebOnly(room *MatrixRoom) bool {
	return room.ConversationID == nil && !room.IsDirect && directoryString(room.RoomType) == ""
}
func groupClientURL(target string, via []string) string {
	q := url.Values{}
	for _, s := range via {
		q.Add("via", s)
	}
	result := "https://matrix.to/#/" + url.PathEscape(target)
	if len(q) > 0 {
		result += "?" + q.Encode()
	}
	return result
}
func publicEntryGroup(hs *HomeServer, e publicDirectoryEntry, server, user string) gin.H {
	target := e.RoomID
	if validGroupAlias(e.CanonicalAlias) {
		target = e.CanonicalAlias
	}
	via := []string{server}
	name := e.Name
	if name == "" {
		name = e.CanonicalAlias
	}
	if name == "" {
		name = "Unnamed group"
	}
	// Keep signed preview URLs bounded even when a remote directory supplies
	// unreasonable metadata. Preview state never authorizes writes.
	e.Name = strings.ToValidUTF8(e.Name[:min(len(e.Name), 384)], "")
	e.Topic = strings.ToValidUTF8(e.Topic[:min(len(e.Topic), 768)], "")
	if len(e.AvatarURL) > 1024 {
		e.AvatarURL = ""
	}
	name = strings.ToValidUTF8(name[:min(len(name), 384)], "")
	out := gin.H{"id": webGroupID(e.RoomID), "name": name, "topic": e.Topic, "membership": "leave", "encryption": "unknown", "join_rule": e.JoinRule, "listed": true, "member_count": e.NumJoinedMembers, "unread_count": 0, "client_url": groupClientURL(target, via), "capabilities": gin.H{}, "target": target, "via": via, "directory_source": server, "room_type": e.RoomType}
	if mediaServer, _, err := webGroupMXC(e.AvatarURL); err == nil && (strings.EqualFold(server, hs.Config.ServerName) || !strings.EqualFold(mediaServer, hs.Config.ServerName)) {
		// An untrusted foreign directory must never mint access to our private
		// local media merely by claiming it is a public room avatar.
		token := encodeDirectoryCursor(hs, directoryBinding("directory-avatar", user), e.AvatarURL)
		out["avatar_url"] = "/api/v1/chat/groups/directory-avatar?ref=" + url.QueryEscape(token)
	}
	preview, _ := json.Marshal(webDirectoryPreview{Entry: e, Server: server})
	out["preview_ref"] = encodeDirectoryCursor(hs, directoryBinding("directory-preview", user), string(preview))
	return out
}

type webDirectoryPreview struct {
	Entry  publicDirectoryEntry `json:"entry"`
	Server string               `json:"server"`
}

func handleWebGroupDirectoryAvatar(c *gin.Context) {
	if !ensureWebGroupIdentity(c) {
		return
	}
	hs := GetHomeServer()
	mxc, err := decodeDirectoryCursor(hs, c.Query("ref"), directoryBinding("directory-avatar", getUserID(c)))
	if err != nil || mxc == "" {
		webGroupFailure(c, 403, "forbidden", "The avatar access token is invalid or expired")
		return
	}
	serveWebGroupMXC(c, hs, mxc, "group-avatar", true)
}

var webDirectoryGate = make(chan struct{}, 8)

func acquireWebDirectory(c *gin.Context) bool {
	select {
	case webDirectoryGate <- struct{}{}:
		return true
	default:
		webGroupFailure(c, 429, "directory_busy", "The group directory is busy, please try again later")
		return false
	}
}
func webDirectoryError(c *gin.Context, err error) {
	status, code, msg := 502, "directory_unavailable", "The group directory is temporarily unavailable, please try again later"
	var remote *federationHTTPError
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		status, code, msg = 504, "directory_timeout", "The group directory query timed out, please try again later"
	} else if errors.As(err, &remote) {
		switch remote.Status {
		case 403:
			status, code, msg = 403, "directory_closed", "The remote server does not publish a group directory"
		case 429:
			status, code, msg = 429, "directory_limited", "Too many queries to the remote server, please try again later"
		}
	} else if strings.Contains(err.Error(), "cursor") {
		status, code, msg = 400, "invalid_cursor", "The page has expired or belongs to another search; please search again"
	}
	webGroupFailure(c, status, code, msg)
}

func handleWebGroupSearch(c *gin.Context) {
	if !ensureWebGroupIdentity(c) {
		return
	}
	hs := GetHomeServer()
	user := getUserID(c)
	scope := c.DefaultQuery("scope", "local")
	server := strings.TrimSpace(c.Query("server"))
	q := strings.TrimSpace(c.Query("q"))
	limit := 30
	if v := c.Query("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 {
			webGroupFailure(c, 400, "invalid_limit", "Invalid page size")
			return
		}
		limit = min(n, 50)
	}
	if len(q) > 256 {
		webGroupFailure(c, 400, "invalid_query", "Search text is too long")
		return
	}
	if scope != "local" && scope != "joined" && scope != "remote" {
		webGroupFailure(c, 400, "invalid_scope", "Invalid search scope")
		return
	}
	if scope == "remote" && !usableServerName(server) {
		webGroupFailure(c, 400, "invalid_server", "Enter a valid Matrix server name")
		return
	}
	if scope != "remote" {
		server = hs.Config.ServerName
	}
	bind := directoryBinding("web", user, scope, server, q)
	position, err := decodeDirectoryCursor(hs, c.Query("cursor"), bind)
	if err != nil {
		webDirectoryError(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
	defer cancel()
	conn := db.GetDB().WithContext(ctx)
	groups := []gin.H{}
	next := ""
	if scope == "joined" {
		query := webGroupRoomQuery(conn).Where("room_id IN (SELECT room_id FROM matrix_room_members WHERE user_id = ? AND membership = ? AND forgotten = ?)", user, MembershipJoin, false).Where("(room_type IS NULL OR room_type = '')")
		if q != "" {
			op := dialect.ILike(conn)
			term := "%" + directorySearchEscape(q) + "%"
			query = query.Where("(name "+op+" ? ESCAPE '!' OR topic "+op+" ? ESCAPE '!')", term, term)
		}
		if position != "" {
			query = query.Where("room_id > ?", position)
		}
		var rooms []MatrixRoom
		if err = query.Order("room_id ASC").Limit(limit + 1).Find(&rooms).Error; err != nil {
			webGroupFailure(c, 500, "internal_error", "Cannot load the group list")
			return
		}
		if len(rooms) > limit {
			rooms = rooms[:limit]
			next = encodeDirectoryCursor(hs, bind, rooms[len(rooms)-1].RoomID)
		}
		groups, err = webGroupDTOs(hs, conn, rooms, user)
		if err != nil {
			webGroupFailure(c, 500, "internal_error", "Cannot load group information")
			return
		}
	} else {
		req := publicDirectoryRequest{Limit: limit}
		req.Filter.GenericSearchTerm = q
		var result *publicDirectoryResponse
		if server == hs.Config.ServerName {
			// Keep the public directory's stable room-id position inside the web cursor.
			if position != "" {
				req.Since = encodeDirectoryCursor(hs, bind, position)
			}
			result, err = localPublicDirectory(ctx, hs, req, bind, true)
			if err == nil && result.NextBatch != "" {
				next = result.NextBatch
			}
		} else {
			if !acquireWebDirectory(c) {
				return
			}
			defer func() { <-webDirectoryGate }()
			req.Since = position
			result, err = newFederationClient(hs).PublicRoomsContext(ctx, server, req)
			if err == nil && result.NextBatch != "" {
				next = encodeDirectoryCursor(hs, bind, result.NextBatch)
			}
		}
		if err != nil {
			webDirectoryError(c, err)
			return
		}
		// A single membership query upgrades metadata for rooms this user actually
		// knows; never expose local private room metadata to an unrelated searcher.
		ids := make([]string, 0, len(result.Chunk))
		for _, e := range result.Chunk {
			ids = append(ids, e.RoomID)
		}
		known := map[string]gin.H{}
		if len(ids) > 0 {
			var rooms []MatrixRoom
			if err = webGroupRoomQuery(conn).Where("room_id IN ? AND room_id IN (SELECT room_id FROM matrix_room_members WHERE user_id = ? AND membership IN ?)", ids, user, []MembershipState{MembershipJoin, MembershipInvite, MembershipKnock}).Find(&rooms).Error; err != nil {
				webGroupFailure(c, 500, "internal_error", "Cannot load the group state")
				return
			}
			views, e := webGroupDTOs(hs, conn, rooms, user)
			if e != nil {
				webGroupFailure(c, 500, "internal_error", "Cannot load group information")
				return
			}
			for i, view := range views {
				known[rooms[i].RoomID] = view
			}
		}
		for _, entry := range result.Chunk {
			if entry.RoomType != "" {
				continue
			}
			dto := publicEntryGroup(hs, entry, server, user)
			if own, ok := known[entry.RoomID]; ok {
				own["target"], own["via"], own["directory_source"], own["preview_ref"] = dto["target"], dto["via"], server, dto["preview_ref"]
				dto = own
			}
			groups = append(groups, dto)
		}
	}
	var cursor any
	if next != "" {
		cursor = next
	}
	c.JSON(200, gin.H{"groups": groups, "next_cursor": cursor, "source": server})
}

type webGroupTargetRequest struct {
	PreviewRef string   `json:"preview_ref"`
	Target     string   `json:"target"`
	Via        []string `json:"via"`
	Action     string   `json:"action"`
}

func bindWebGroupTarget(c *gin.Context) (webGroupTargetRequest, bool) {
	var req webGroupTargetRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 24<<10)
	if c.ShouldBindJSON(&req) != nil {
		webGroupFailure(c, 400, "invalid_request", "Invalid group address request")
		return req, false
	}
	target, via, err := parseWebGroupTarget(req.Target, req.Via)
	if err != nil {
		webGroupFailure(c, 400, "invalid_target", err.Error())
		return req, false
	}
	req.Target, req.Via = target, via
	return req, true
}
func handleWebGroupResolve(c *gin.Context) {
	if !ensureWebGroupIdentity(c) {
		return
	}
	req, ok := bindWebGroupTarget(c)
	if !ok {
		return
	}
	if !acquireWebDirectory(c) {
		return
	}
	defer func() { <-webDirectoryGate }()
	hs := GetHomeServer()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
	defer cancel()
	target, via, merr := resolveWebGroupTarget(ctx, hs, req.Target, req.Via)
	if merr != nil {
		webGroupError(c, merr)
		return
	}
	out := gin.H{"target": target, "via": via, "client_url": groupClientURL(target, via)}
	if req.PreviewRef != "" {
		raw, e := decodeDirectoryCursor(hs, req.PreviewRef, directoryBinding("directory-preview", getUserID(c)))
		var preview webDirectoryPreview
		if e != nil || json.Unmarshal([]byte(raw), &preview) != nil || preview.Entry.RoomID != target || !usableServerName(preview.Server) {
			webGroupFailure(c, 400, "invalid_preview", "The group preview has expired; please search again")
			return
		}
		out["group"] = publicEntryGroup(hs, preview.Entry, preview.Server, getUserID(c))
		if !slices.Contains(via, preview.Server) {
			via = append(via, preview.Server)
			out["via"] = via
		}
	}
	var room MatrixRoom
	conn := db.GetDB().WithContext(ctx)
	err := conn.Where("room_id = ?", target).First(&room).Error
	if err == nil {
		allowed, e := webGroupRoomAllowed(conn, &room)
		if e != nil {
			webGroupError(c, ErrInternal)
			return
		}
		if !allowed || !directoryWebOnly(&room) {
			webGroupFailure(c, 422, "unsupported_group", "This address is not a supported chat group; use a client")
			return
		}
		var member MatrixRoomMember
		mine := conn.Where("room_id = ? AND user_id = ? AND membership IN ?", target, getUserID(c), []MembershipState{MembershipJoin, MembershipInvite, MembershipKnock}).First(&member).Error == nil
		if mine {
			if dto, e := webGroupDTO(hs, conn, &room, getUserID(c)); e == nil {
				out["group"] = dto
			} else {
				webGroupFailure(c, 500, "internal_error", "Cannot load group information")
				return
			}
		} else if room.Visibility == "public" {
			entry := publicDirectoryEntry{RoomID: target, Name: directoryString(room.Name), Topic: directoryString(room.Topic), JoinRule: room.JoinRule}
			out["group"] = publicEntryGroup(hs, entry, hs.Config.ServerName, getUserID(c))
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		webGroupFailure(c, 500, "internal_error", "Cannot look up the group address")
		return
	}
	c.JSON(200, out)
}

func handleWebGroupJoin(c *gin.Context) {
	if !ensureWebGroupIdentity(c) {
		return
	}
	req, ok := bindWebGroupTarget(c)
	if !ok {
		return
	}
	if req.Action != "" && req.Action != "join" && req.Action != "knock" {
		webGroupFailure(c, 400, "invalid_action", "Invalid join rule")
		return
	}
	hs := GetHomeServer()
	ctx, cancel := context.WithTimeout(c.Request.Context(), joinSettingsSource().Total)
	defer cancel()
	target, via, merr := resolveWebGroupTarget(ctx, hs, req.Target, req.Via)
	if merr != nil {
		webGroupError(c, merr)
		return
	}
	var room MatrixRoom
	if err := db.GetDB().WithContext(ctx).Where("room_id = ?", target).First(&room).Error; err == nil {
		allowed, e := webGroupRoomAllowed(db.GetDB().WithContext(ctx), &room)
		if e != nil {
			webGroupError(c, ErrInternal)
			return
		}
		if !allowed || !directoryWebOnly(&room) {
			webGroupFailure(c, 422, "unsupported_group", "This address is not a supported chat group; use a client")
			return
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		webGroupError(c, ErrInternal)
		return
	}
	if req.Action == "knock" {
		target, merr = hs.Room.KnockRoomContext(ctx, getUserID(c), target, via, "")
	} else {
		target, merr = hs.Room.JoinRoomContext(ctx, getUserID(c), target, &JoinRoomRequest{Servers: via})
	}
	if merr != nil {
		webGroupError(c, merr)
		return
	}
	if db.GetDB().Where("room_id = ?", target).First(&room).Error != nil {
		webGroupFailure(c, 503, "join_unconfirmed", "The action was submitted but the group state is not available yet; refresh later")
		return
	}
	// Unknown external spaces can only be identified after state is received. Do
	// not lie about membership or silently leave; give the client handoff instead.
	if !directoryWebOnly(&room) {
		c.JSON(200, gin.H{"status": "joined", "client_required": true, "client_url": groupClientURL(target, via), "reason": "unsupported_group"})
		return
	}
	renderWebGroupJoinStatus(c, hs, &room, getUserID(c))
}
func renderWebGroupJoinStatus(c *gin.Context, hs *HomeServer, room *MatrixRoom, user string) {
	dto, err := webGroupDTO(hs, db.GetDB().WithContext(c.Request.Context()), room, user)
	if err != nil {
		webGroupFailure(c, 500, "internal_error", "Cannot load the group state")
		return
	}
	status := "joined"
	if dto["membership"] == "knock" || dto["membership"] == MembershipKnock {
		status = "knock"
	}
	out := gin.H{"group": dto, "status": status}
	if _, _, partial := roomPartialState(db.GetDB(), room.RoomID); partial {
		out["status"] = "joining"
		out["operation_id"] = webGroupID(room.RoomID)
	}
	c.JSON(200, out)
}
func handleWebGroupJoinStatus(c *gin.Context) {
	if !ensureWebGroupIdentity(c) {
		return
	}
	id, err := webGroupRoomID(c.Param("operationID"))
	if err != nil {
		webGroupFailure(c, 404, "not_found", "Join operation not found")
		return
	}
	hs := GetHomeServer()
	var member MatrixRoomMember
	if db.GetDB().Where("room_id = ? AND user_id = ? AND membership IN ?", id, getUserID(c), []MembershipState{MembershipJoin, MembershipKnock}).First(&member).Error != nil {
		webGroupFailure(c, 404, "not_found", "Join operation not found")
		return
	}
	var room MatrixRoom
	if db.GetDB().Where("room_id = ?", id).First(&room).Error != nil {
		webGroupFailure(c, 404, "not_found", "Group not found")
		return
	}
	allowed, e := webGroupRoomAllowed(db.GetDB().WithContext(c.Request.Context()), &room)
	if e != nil {
		webGroupError(c, ErrInternal)
		return
	}
	if !allowed {
		webGroupFailure(c, 404, "not_found", "Join operation not found")
		return
	}
	renderWebGroupJoinStatus(c, hs, &room, getUserID(c))
}
