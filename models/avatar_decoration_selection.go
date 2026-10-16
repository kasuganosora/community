package models

import (
	"database/sql/driver"
	"encoding/json"
	"math"
	"strconv"
)

// MaxAvatarDecorations mirrors Misskey's hard cap on i/update
// (avatarDecorations maxItems: 16). The effective per-user limit is an
// instance setting at or below it.
const MaxAvatarDecorations = 16

// AvatarDecorationSelection is one decoration placed on an avatar, in
// Misskey's geometry: angle is a fraction of a full turn (-0.5..0.5),
// offsets are fractions of the decoration's own width/height (-0.25..0.25).
// URL is empty for local decorations (resolved by ID) and holds the origin
// server's image URL for decorations copied from a remote Misskey user.
type AvatarDecorationSelection struct {
	ID      string  `json:"id"`
	URL     string  `json:"url,omitempty"`
	Angle   float64 `json:"angle,omitempty"`
	FlipH   bool    `json:"flip_h,omitempty"`
	OffsetX float64 `json:"offset_x,omitempty"`
	OffsetY float64 `json:"offset_y,omitempty"`
}

func clampDecorationValue(v, limit float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	v = math.Max(-limit, math.Min(limit, v))
	// Keep stored JSON short and stable.
	r, _ := strconv.ParseFloat(strconv.FormatFloat(v, 'f', 4, 64), 64)
	return r
}

// Normalized clamps the geometry into Misskey's accepted ranges.
func (d AvatarDecorationSelection) Normalized() AvatarDecorationSelection {
	d.Angle = clampDecorationValue(d.Angle, 0.5)
	d.OffsetX = clampDecorationValue(d.OffsetX, 0.25)
	d.OffsetY = clampDecorationValue(d.OffsetY, 0.25)
	return d
}

// LocalID returns the numeric ID of a local decoration, or 0.
func (d AvatarDecorationSelection) LocalID() uint64 {
	if d.URL != "" {
		return 0
	}
	id, err := strconv.ParseUint(d.ID, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// AvatarDecorationSelections is stored as a JSON array in a TEXT column
// (portable across PostgreSQL, MySQL and SQLite). NULL means none.
type AvatarDecorationSelections []AvatarDecorationSelection

func (s AvatarDecorationSelections) Value() (driver.Value, error) {
	if len(s) == 0 {
		return nil, nil
	}
	b, err := json.Marshal([]AvatarDecorationSelection(s))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (s *AvatarDecorationSelections) Scan(v any) error {
	var raw []byte
	switch x := v.(type) {
	case nil:
		*s = nil
		return nil
	case []byte:
		raw = x
	case string:
		raw = []byte(x)
	default:
		*s = nil
		return nil
	}
	var out []AvatarDecorationSelection
	if len(raw) == 0 || json.Unmarshal(raw, &out) != nil {
		// A corrupt value only hides decorations; it must not break account loads.
		*s = nil
		return nil
	}
	if len(out) > MaxAvatarDecorations {
		out = out[:MaxAvatarDecorations]
	}
	*s = out
	return nil
}

// EffectiveAvatarDecorations returns the account's decorations, upgrading the
// legacy single avatar_decoration_id selection when no list is stored.
func (a *Account) EffectiveAvatarDecorations() AvatarDecorationSelections {
	if len(a.AvatarDecorations) > 0 {
		return a.AvatarDecorations
	}
	if a.AvatarDecorationID != 0 && a.IsLocal() {
		return AvatarDecorationSelections{{ID: strconv.FormatUint(a.AvatarDecorationID, 10)}}
	}
	return nil
}
