package indexsync

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/systemsettings"
)

// Meilisearch is memory hungry: a backfill that pushes large batches or many
// writers at once can push it over a small container limit. Every write from
// this package therefore goes through a small slot pool (default one writer),
// the backfill uses small batches, and progress is checkpointed after each
// batch Meili has acknowledged so a crash or restart resumes instead of
// starting over. Search keeps answering throughout: a partial index simply
// returns fewer hits and an unreachable engine falls back to the database.

const checkpointKey = "search.reindex_checkpoint"

var (
	writeSlots     = make(chan struct{}, envInt("SEARCH_INDEX_CONCURRENCY", 1, 1, 16))
	reindexBatch   = envInt("SEARCH_REINDEX_BATCH_SIZE", 100, 1, 1000)
	reindexPause   = time.Duration(envInt("SEARCH_REINDEX_PAUSE_MS", 0, 0, 60000)) * time.Millisecond
	progressPeriod = 5 * time.Second

	// checkpoint persistence is swapped in tests.
	loadCheckpoint = func() map[string]uint64 {
		out := map[string]uint64{}
		raw := systemsettings.GetString(checkpointKey, "")
		if raw != "" {
			_ = json.Unmarshal([]byte(raw), &out)
		}
		return out
	}
	saveCheckpoint = func(cp map[string]uint64) {
		b, _ := json.Marshal(cp)
		systemsettings.Set(checkpointKey, string(b))
		if err := systemsettings.SaveToDB(checkpointKey); err != nil {
			log.Warn("search index checkpoint not saved: %v", err)
		}
	}
)

func envInt(name string, def, min, max int) int {
	v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// withWriteSlot runs fn while holding one of the Meili write slots.
func withWriteSlot(fn func() error) error {
	writeSlots <- struct{}{}
	defer func() { <-writeSlots }()
	return fn()
}

// IndexProgress is the state of one index backfill.
type IndexProgress struct {
	Index    string    `json:"index"`
	Done     int64     `json:"done"`
	Total    int64     `json:"total"`
	Running  bool      `json:"running"`
	Finished bool      `json:"finished"`
	Started  time.Time `json:"started_at"`
	Updated  time.Time `json:"updated_at"`
	start    int64
	lastLog  time.Time
}

var (
	progressMu sync.Mutex
	progress   = map[string]*IndexProgress{}
	order      = []string{"accounts", "statuses", "tags"}
)

// Progress returns the search index backfill state ("search index done/total").
func Progress() []IndexProgress {
	progressMu.Lock()
	defer progressMu.Unlock()
	out := make([]IndexProgress, 0, len(order))
	for _, name := range order {
		if p := progress[name]; p != nil {
			out = append(out, *p)
		}
	}
	return out
}

func beginProgress(index string, done, total int64) {
	progressMu.Lock()
	now := time.Now()
	p := &IndexProgress{Index: index, Done: done, Total: total, Running: true, Started: now, Updated: now, start: done}
	progress[index] = p
	line := p.line()
	progressMu.Unlock()
	log.Info("%s", line)
}

func advanceProgress(index string, n int64, finished bool) {
	progressMu.Lock()
	p := progress[index]
	if p == nil {
		progressMu.Unlock()
		return
	}
	p.Done += n
	if p.Done > p.Total {
		p.Total = p.Done
	}
	p.Updated = time.Now()
	if finished {
		p.Running, p.Finished = false, true
	}
	var line string
	if finished || time.Since(p.lastLog) >= progressPeriod {
		p.lastLog = time.Now()
		line = p.line()
		if finished {
			line += " finished"
		}
	}
	progressMu.Unlock()
	if line != "" {
		log.Info("%s", line)
	}
}

func stopProgress(index string) {
	progressMu.Lock()
	defer progressMu.Unlock()
	if p := progress[index]; p != nil {
		p.Running = false
	}
}

// line renders the same "progress stage=... done=x/y" format as the migration tools.
func (p *IndexProgress) line() string {
	elapsed := time.Since(p.Started).Seconds()
	if elapsed <= 0 {
		elapsed = 1e-6
	}
	rate := float64(p.Done-p.start) / elapsed
	s := fmt.Sprintf("progress stage=search-index/%s done=%d/%d", p.Index, p.Done, p.Total)
	if p.Total > 0 {
		s += fmt.Sprintf(" (%.1f%%)", 100*float64(p.Done)/float64(p.Total))
		if rate > 0 && p.Total > p.Done {
			s += " eta=" + (time.Duration(float64(p.Total-p.Done)/rate) * time.Second).String()
		}
	}
	return s + fmt.Sprintf(" speed=%.0f/s", rate)
}
