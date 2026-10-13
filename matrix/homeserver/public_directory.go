package homeserver

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/db/dialect"
	"gorm.io/gorm"
)

const publicDirectoryMaxLimit = 250

type publicDirectoryRequest struct {
	Limit  int    `json:"limit"`
	Since  string `json:"since,omitempty"`
	Filter struct {
		GenericSearchTerm string `json:"generic_search_term,omitempty"`
	} `json:"filter"`
}
type publicDirectoryEntry struct {
	RoomID           string `json:"room_id"`
	Name             string `json:"name,omitempty"`
	Topic            string `json:"topic,omitempty"`
	AvatarURL        string `json:"avatar_url,omitempty"`
	CanonicalAlias   string `json:"canonical_alias,omitempty"`
	NumJoinedMembers int64  `json:"num_joined_members"`
	WorldReadable    bool   `json:"world_readable"`
	GuestCanJoin     bool   `json:"guest_can_join"`
	JoinRule         string `json:"join_rule,omitempty"`
	RoomType         string `json:"room_type,omitempty"`
}
type publicDirectoryResponse struct {
	Chunk                  []publicDirectoryEntry `json:"chunk"`
	NextBatch              string                 `json:"next_batch,omitempty"`
	TotalRoomCountEstimate int64                  `json:"total_room_count_estimate"`
}

// Cursors are signed and bound to the query, scope, server and requesting user.
// Raw remote continuation tokens never become a public directory URL.
type groupDirectoryCursor struct {
	Bind     string `json:"b"`
	Position string `json:"p"`
	Expires  int64  `json:"e"`
}

func encodeDirectoryCursor(hs *HomeServer, bind, position string) string {
	raw, _ := json.Marshal(groupDirectoryCursor{Bind: bind, Position: position, Expires: time.Now().Add(24 * time.Hour).Unix()})
	mac := hmac.New(sha256.New, hs.privateKey)
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func decodeDirectoryCursor(hs *HomeServer, token, bind string) (string, error) {
	if token == "" {
		return "", nil
	}
	if len(token) > 16384 {
		return "", errors.New("invalid cursor")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return "", errors.New("invalid cursor")
	}
	raw, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return "", errors.New("invalid cursor")
	}
	signature, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return "", errors.New("invalid cursor")
	}
	mac := hmac.New(sha256.New, hs.privateKey)
	_, _ = mac.Write(raw)
	var c groupDirectoryCursor
	if !hmac.Equal(mac.Sum(nil), signature) || json.Unmarshal(raw, &c) != nil || c.Bind != bind || c.Expires < time.Now().Unix() {
		return "", errors.New("invalid or expired cursor")
	}
	return c.Position, nil
}
func directoryBinding(parts ...string) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func parsePublicDirectoryRequest(c *gin.Context) (publicDirectoryRequest, *MatrixError) {
	req := publicDirectoryRequest{Limit: 100, Since: c.Query("since")}
	if c.Request.Method == http.MethodPost {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		if c.ShouldBindJSON(&req) != nil {
			return req, &MatrixError{ErrCode: "M_BAD_JSON", Error: "Invalid directory request"}
		}
	}
	if v := c.Query("limit"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil {
			return req, &MatrixError{ErrCode: "M_INVALID_PARAM", Error: "Invalid page size"}
		}
		req.Limit = n
	}
	if req.Limit <= 0 {
		req.Limit = 100
	}
	req.Limit = min(req.Limit, publicDirectoryMaxLimit)
	req.Filter.GenericSearchTerm = strings.TrimSpace(req.Filter.GenericSearchTerm)
	if len(req.Filter.GenericSearchTerm) > 256 || len(req.Since) > 16384 {
		return req, &MatrixError{ErrCode: "M_INVALID_PARAM", Error: "Directory query is too long"}
	}
	return req, nil
}

func localPublicDirectory(ctx context.Context, hs *HomeServer, req publicDirectoryRequest, bind string, webOnly bool) (*publicDirectoryResponse, error) {
	after, err := decodeDirectoryCursor(hs, req.Since, bind)
	if err != nil {
		return nil, err
	}
	conn := db.GetDB().WithContext(ctx)
	// Listing is explicitly opt-in, independent of whether the room allows public joins.
	query := conn.Model(&MatrixRoom{}).Where("visibility = ?", "public")
	if webOnly {
		query = webGroupRoomQuery(query).Where("(room_type IS NULL OR room_type = '')")
	}
	if term := req.Filter.GenericSearchTerm; term != "" {
		term = "%" + directorySearchEscape(term) + "%"
		op := dialect.ILike(conn)
		query = query.Where("(name "+op+" ? ESCAPE '!' OR topic "+op+" ? ESCAPE '!' OR room_id IN (SELECT room_id FROM matrix_room_aliases WHERE room_alias "+op+" ? ESCAPE '!'))", term, term, term)
	}
	var total int64
	if err = query.Count(&total).Error; err != nil {
		return nil, err
	}
	if after != "" {
		query = query.Where("room_id > ?", after)
	}
	var rooms []MatrixRoom
	if err = query.Order("room_id ASC").Limit(req.Limit + 1).Find(&rooms).Error; err != nil {
		return nil, err
	}
	more := len(rooms) > req.Limit
	if more {
		rooms = rooms[:req.Limit]
	}
	result := &publicDirectoryResponse{Chunk: []publicDirectoryEntry{}, TotalRoomCountEstimate: total}
	if len(rooms) == 0 {
		return result, nil
	}
	ids := make([]string, len(rooms))
	for i, r := range rooms {
		ids[i] = r.RoomID
	}
	var counts []struct {
		RoomID string
		N      int64
	}
	if err = conn.Model(&MatrixRoomMember{}).Select("room_id, COUNT(*) AS n").Where("room_id IN ? AND membership = ?", ids, MembershipJoin).Group("room_id").Find(&counts).Error; err != nil {
		return nil, err
	}
	countByID := map[string]int64{}
	for _, c := range counts {
		countByID[c.RoomID] = c.N
	}
	var aliases []MatrixRoomState
	if err = conn.Where("room_id IN ? AND event_type = ? AND state_key = ?", ids, EventTypeRoomCanonicalAlias, "").Find(&aliases).Error; err != nil {
		return nil, err
	}
	aliasByID := map[string]string{}
	for _, a := range aliases {
		aliasByID[a.RoomID], _ = a.Content["alias"].(string)
	}
	for _, r := range rooms {
		entry := publicDirectoryEntry{RoomID: r.RoomID, Name: directoryString(r.Name), Topic: directoryString(r.Topic), AvatarURL: directoryString(r.AvatarURL), CanonicalAlias: aliasByID[r.RoomID], NumJoinedMembers: countByID[r.RoomID], WorldReadable: r.HistoryVisibility == "world_readable", GuestCanJoin: r.GuestAccess == "can_join", JoinRule: r.JoinRule, RoomType: directoryString(r.RoomType)}
		result.Chunk = append(result.Chunk, entry)
	}
	if more {
		result.NextBatch = encodeDirectoryCursor(hs, bind, rooms[len(rooms)-1].RoomID)
	}
	return result, nil
}
func directoryString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func (c *FederationClient) PublicRoomsContext(ctx context.Context, server string, req publicDirectoryRequest) (*publicDirectoryResponse, error) {
	if !usableServerName(server) {
		return nil, errors.New("invalid server name")
	}
	endpoint := c.federationBaseURL(server) + "/_matrix/federation/v1/publicRooms"
	response, body, err := c.doFederationRequestContext(ctx, http.MethodPost, endpoint, server, req, 2<<20)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, &federationHTTPError{Status: response.StatusCode, Message: string(body)}
	}
	var out publicDirectoryResponse
	if err = json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if len(out.Chunk) > req.Limit || len(out.NextBatch) > 8192 {
		return nil, errors.New("invalid remote directory page")
	}
	seen := map[string]bool{}
	entries := make([]publicDirectoryEntry, 0, len(out.Chunk))
	for _, entry := range out.Chunk {
		if validGroupRoomID(entry.RoomID) && !seen[entry.RoomID] {
			entries = append(entries, entry)
			seen[entry.RoomID] = true
		}
	}
	out.Chunk = entries
	return &out, nil
}

func handlePublicDirectory(c *gin.Context, allowRemote bool) {
	hs := GetHomeServer()
	req, merr := parsePublicDirectoryRequest(c)
	if merr != nil {
		c.JSON(400, merr)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 12*time.Second)
	defer cancel()
	server := strings.TrimSpace(c.Query("server"))
	var result *publicDirectoryResponse
	var err error
	if allowRemote && server != "" && server != hs.Config.ServerName {
		if !usableServerName(server) {
			c.JSON(400, MatrixError{ErrCode: "M_INVALID_PARAM", Error: "Invalid server name"})
			return
		}
		result, err = newFederationClient(hs).PublicRoomsContext(ctx, server, req)
	} else {
		bind := directoryBinding("matrix-public", getUserID(c), c.GetString("origin"), req.Filter.GenericSearchTerm)
		result, err = localPublicDirectory(ctx, hs, req, bind, false)
	}
	if err != nil {
		if strings.Contains(err.Error(), "cursor") {
			c.JSON(400, MatrixError{ErrCode: "M_INVALID_PARAM", Error: "Invalid or expired pagination cursor"})
			return
		}
		if errors.Is(err, gorm.ErrRecordNotFound) {
			c.JSON(404, ErrNotFound)
			return
		}
		var remote *federationHTTPError
		if errors.As(err, &remote) {
			status := http.StatusBadGateway
			if remote.Status == 403 || remote.Status == 429 {
				status = remote.Status
			}
			c.JSON(status, MatrixError{ErrCode: "M_BAD_GATEWAY", Error: "Room directory is unavailable"})
			return
		}
		c.JSON(http.StatusBadGateway, MatrixError{ErrCode: "M_BAD_GATEWAY", Error: "Unable to read room directory"})
		return
	}
	c.JSON(http.StatusOK, result)
}

func directorySearchEscape(term string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(term)
}
