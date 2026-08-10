package models

import (
	"testing"
	"time"
)

// pastTime returns a time.Time that is safely in the past (1 hour ago).
func pastTime(t *testing.T) time.Time {
	t.Helper()
	return time.Now().Add(-1 * time.Hour)
}

// futureTime returns a time.Time that is safely in the future (1 hour ahead).
func futureTime(t *testing.T) time.Time {
	t.Helper()
	return time.Now().Add(1 * time.Hour)
}
