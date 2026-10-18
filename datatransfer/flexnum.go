package datatransfer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
)

// flexInt accepts JSON integers, floats (rounded half away from zero), numeric
// strings and null. Source platforms store image dimensions and durations as
// floats (Misskey drive_file.properties width 1665.517 in the maid.lat
// rehearsal); rejecting the whole bundle for that is never useful.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*f = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s == "" {
			*f = 0
			return nil
		}
		b = []byte(s)
	}
	if i, err := strconv.ParseInt(string(b), 10, 64); err == nil {
		*f = flexInt(i)
		return nil
	}
	v, err := strconv.ParseFloat(string(b), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v > math.MaxInt64 || v < math.MinInt64 {
		return fmt.Errorf("invalid number %q", string(b))
	}
	*f = flexInt(math.Round(v))
	return nil
}

// UnmarshalJSON tolerates float or string numbers in size/width/height.
func (m *Media) UnmarshalJSON(b []byte) error {
	type plain Media
	aux := struct {
		*plain
		Size   flexInt `json:"size"`
		Width  flexInt `json:"width,omitempty"`
		Height flexInt `json:"height,omitempty"`
	}{plain: (*plain)(m)}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	m.Size, m.Width, m.Height = int64(aux.Size), int(aux.Width), int(aux.Height)
	return nil
}
