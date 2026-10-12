package homeserver

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
)

// sendJoinResponse is a send_join answer whose state and auth chain are kept
// as raw per-event JSON. The (potentially 100k-event) arrays are read one
// element at a time instead of materialising the whole document as
// map[string]any, which multiplied memory use several times over.
type sendJoinResponse struct {
	Origin         string
	Event          json.RawMessage
	State          []json.RawMessage
	AuthChain      []json.RawMessage
	MembersOmitted bool
	// ServersInRoom is the (untrusted, bounded) list of servers the resident
	// server says are in the room; MSC3706 sends it with members_omitted.
	ServersInRoom []string
}

// maxServersInRoom bounds how much of servers_in_room is kept.
const maxServersInRoom = 2000

// sendJoinBodyLimit is the largest send_join response accepted, in bytes.
// MATRIX_FEDERATION_SEND_JOIN_MAX_MB overrides the 64 MiB default (1..200).
func sendJoinBodyLimit() int64 {
	mb := int64(64)
	if v, err := strconv.ParseInt(os.Getenv("MATRIX_FEDERATION_SEND_JOIN_MAX_MB"), 10, 64); err == nil && v >= 1 && v <= 200 {
		mb = v
	}
	return mb << 20
}

const defaultMaxJoinStateEvents = 250000

const maxSendJoinEvents = defaultMaxJoinStateEvents

// sendJoinEventCap is the most state + auth chain events a join response may
// carry (MATRIX_MAX_JOIN_STATE_EVENTS, default 250000). It is enforced while
// the response is being read, so an oversized room is refused up front instead
// of failing halfway through persistence. Replaceable in tests.
var sendJoinEventCap = func() int {
	return envInt("MATRIX_MAX_JOIN_STATE_EVENTS", defaultMaxJoinStateEvents, 1, 5_000_000)
}

// sendJoinTooManyEventsError reports a join response beyond the configured cap.
// It matches errFederationResponseTooLarge so callers never retry it on another
// protocol version.
type sendJoinTooManyEventsError struct{ limit int }

func (e *sendJoinTooManyEventsError) Error() string {
	return fmt.Sprintf("too many send_join events: more than %d", e.limit)
}
func (e *sendJoinTooManyEventsError) Is(target error) bool {
	return target == errFederationResponseTooLarge
}

func joinStateTooLargeError(limit int) *MatrixError {
	return &MatrixError{ErrCode: "M_TOO_LARGE", Error: fmt.Sprintf("This room is too large to join through this server: its state has more than %d events (limit set by MATRIX_MAX_JOIN_STATE_EVENTS).", limit)}
}

func decodeSendJoinResponse(r io.Reader) (*sendJoinResponse, error) {
	dec := json.NewDecoder(bufio.NewReaderSize(r, 64<<10))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	wrapped := false
	if d, ok := tok.(json.Delim); ok && d == '[' {
		wrapped = true
		// v1 envelope: [200, {...}]
		var status int
		if err = dec.Decode(&status); err != nil {
			return nil, err
		}
		if status != 200 {
			return nil, errors.New("send_join v1 envelope reports failure")
		}
		if tok, err = dec.Token(); err != nil {
			return nil, err
		}
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, errors.New("send_join response is not a JSON object")
	}
	res := &sendJoinResponse{}
	seen := map[string]bool{}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		if seen[key] {
			return nil, fmt.Errorf("duplicate send_join field %s", key)
		}
		seen[key] = true
		switch key {
		case "state", "auth_chain", "pdus":
			list, err := decodeRawArray(dec)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			if key == "state" || key == "pdus" { // "pdus" is the /state spelling
				res.State = list
			} else {
				res.AuthChain = list
			}
			if limit := sendJoinEventCap(); len(res.State)+len(res.AuthChain) > limit {
				return nil, &sendJoinTooManyEventsError{limit: limit}
			}
		case "event":
			if err = dec.Decode(&res.Event); err != nil {
				return nil, err
			}
		case "origin":
			if err = dec.Decode(&res.Origin); err != nil {
				return nil, err
			}
		case "members_omitted":
			if err = dec.Decode(&res.MembersOmitted); err != nil {
				return nil, err
			}
		case "servers_in_room":
			var rawServers json.RawMessage
			if err = dec.Decode(&rawServers); err != nil {
				return nil, err
			}
			// Only a hint: a malformed list is ignored instead of failing the join.
			var servers []string
			if json.Unmarshal(rawServers, &servers) == nil {
				if len(servers) > maxServersInRoom {
					servers = servers[:maxServersInRoom]
				}
				res.ServersInRoom = servers
			}
		default:
			var skip json.RawMessage
			if err = dec.Decode(&skip); err != nil {
				return nil, err
			}
		}
	}
	if _, err = dec.Token(); err != nil { // closing '}'
		return nil, err
	}
	if wrapped {
		end, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if end != json.Delim(']') {
			return nil, errors.New("invalid send_join v1 envelope")
		}
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("trailing data after send_join response")
	}
	return res, nil
}

func decodeRawArray(dec *json.Decoder) ([]json.RawMessage, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if tok == nil {
		return nil, nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, errors.New("expected an array")
	}
	out := []json.RawMessage{}
	for dec.More() {
		if limit := sendJoinEventCap(); len(out) >= limit {
			return nil, &sendJoinTooManyEventsError{limit: limit}
		}
		var raw json.RawMessage
		if err = dec.Decode(&raw); err != nil {
			return nil, err
		}
		if len(raw) > 65536 {
			return nil, errors.New("send_join event exceeds Matrix event size limit")
		}
		out = append(out, raw)
	}
	if _, err = dec.Token(); err != nil {
		return nil, err
	}
	return out, nil
}

// asMap converts to the loosely typed shape used by processFederationJoinResponse
// callers and tests.
func (s *sendJoinResponse) asMap() (map[string]any, error) {
	out := map[string]any{"origin": s.Origin, "members_omitted": s.MembersOmitted}
	if s.MembersOmitted {
		out["servers_in_room"] = s.ServersInRoom
	}
	conv := func(list []json.RawMessage) ([]any, error) {
		res := make([]any, 0, len(list))
		for _, raw := range list {
			var v any
			if err := json.Unmarshal(raw, &v); err != nil {
				return nil, err
			}
			res = append(res, v)
		}
		return res, nil
	}
	var err error
	if out["state"], err = conv(s.State); err != nil {
		return nil, err
	}
	if out["auth_chain"], err = conv(s.AuthChain); err != nil {
		return nil, err
	}
	if len(s.Event) > 0 {
		var ev any
		if err = json.Unmarshal(s.Event, &ev); err != nil {
			return nil, err
		}
		out["event"] = ev
	}
	return out, nil
}
