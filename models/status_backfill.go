package models

import "time"

// StatusPropBackfilled marks a remote status that was imported long after it
// was published (a boosted original, reply context, a pasted URL, a delayed
// delivery). Statuses get autoincrement ids, so such a post would otherwise
// sort above everything that arrived since its publish date. Backfilled
// statuses stay reachable by URL, profile and thread but are not placed in the
// public, tag, home or list timelines.
const StatusPropBackfilled = "backfilled"

const (
	// BackfillFetchAge: a status we fetched on demand is "live" if it was
	// published within this window (e.g. the original of a fresh boost).
	BackfillFetchAge = time.Hour
	// BackfillDeliveryAge: a status pushed to our inbox is "live" unless it
	// was published much earlier (a server catching up after an outage).
	BackfillDeliveryAge = 48 * time.Hour
)

// ShouldBackfill reports whether a status published at published and stored at
// arrived should be kept out of timelines.
func ShouldBackfill(published, arrived time.Time, fetched bool) bool {
	maxAge := BackfillDeliveryAge
	if fetched {
		maxAge = BackfillFetchAge
	}

	return arrived.Sub(published) > maxAge
}

// IsBackfilled reports whether the status carries the backfill marker.
func (s *Status) IsBackfilled() bool {
	if s == nil {
		return false
	}

	return s.Properties[StatusPropBackfilled] == "1"
}

// MarkBackfilled sets the backfill marker.
func (s *Status) MarkBackfilled() {
	if s.Properties == nil {
		s.Properties = map[string]any{}
	}

	s.Properties[StatusPropBackfilled] = "1"
}
