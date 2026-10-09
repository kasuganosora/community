package serializer

import (
	"encoding/json"
	"time"
)

// APITime marshals as ISO 8601 with exactly three fractional digits and a Z
// suffix ("2025-01-01T00:00:00.000Z"), the format Mastodon emits and strict
// iOS decoders accept.
type APITime struct{ time.Time }

const apiTimeLayout = "2006-01-02T15:04:05.000Z07:00"

// NewAPITime wraps t (nil stays nil).
func NewAPITime(t *time.Time) *APITime {
	if t == nil {
		return nil
	}

	return &APITime{t.UTC()}
}

// newAPIDayTime mirrors Mastodon's Account#created_at, which is rounded down
// to midnight UTC.
func newAPIDayTime(t *time.Time) *APITime {
	if t == nil {
		return nil
	}

	u := t.UTC()

	return &APITime{time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC)}
}

func (t APITime) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.UTC().Format(apiTimeLayout))
}

func (t *APITime) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}

	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return err
	}

	t.Time = parsed

	return nil
}

// APIDate marshals as a bare date ("2025-01-01"), as Mastodon does for
// Account#last_status_at.
type APIDate struct{ time.Time }

func newAPIDate(t *time.Time) *APIDate {
	if t == nil {
		return nil
	}

	return &APIDate{t.UTC()}
}

func (d APIDate) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.UTC().Format("2006-01-02"))
}

func (d *APIDate) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}

	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		parsed, err = time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return err
		}
	}

	d.Time = parsed

	return nil
}
