package indexsync

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCommitBatchCheckpointsOnlyAcknowledgedBatches(t *testing.T) {
	saved := []map[string]uint64{}
	prev := saveCheckpoint
	saveCheckpoint = func(cp map[string]uint64) {
		c := map[string]uint64{}
		for k, v := range cp {
			c[k] = v
		}
		saved = append(saved, c)
	}
	defer func() { saveCheckpoint = prev }()
	beginProgress("statuses", 40, 100)
	cp := map[string]uint64{"statuses": 40}
	if err := commitBatch("statuses", cp, 50, 10, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := commitBatch("statuses", cp, 60, 10, func() error { return errors.New("meili oom") }); err == nil {
		t.Fatal("expected failure")
	}
	if len(saved) != 1 || saved[0]["statuses"] != 50 || cp["statuses"] != 50 {
		t.Fatalf("checkpoint must only advance after an acknowledged batch: %v %v", saved, cp)
	}
	var got IndexProgress
	for _, p := range Progress() {
		if p.Index == "statuses" {
			got = p
		}
	}
	if got.Done != 50 || got.Total != 100 || !got.Running {
		t.Fatalf("progress resumes from checkpoint: %+v", got)
	}
	if line := got.line(); !strings.HasPrefix(line, "progress stage=search-index/statuses done=50/100 (50.0%)") {
		t.Fatalf("unexpected progress line %q", line)
	}
}

func TestWriteSlotsLimitConcurrency(t *testing.T) {
	if cap(writeSlots) != 1 {
		t.Skip("SEARCH_INDEX_CONCURRENCY overridden")
	}
	inside := make(chan struct{})
	release := make(chan struct{})
	go func() { _ = withWriteSlot(func() error { close(inside); <-release; return nil }) }()
	<-inside
	second := make(chan struct{})
	go func() { _ = withWriteSlot(func() error { close(second); return nil }) }()
	select {
	case <-second:
		t.Fatal("a second writer ran while the only slot was held")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-second
}

func TestEnvIntBounds(t *testing.T) {
	t.Setenv("X_TEST_INT", "0")
	if envInt("X_TEST_INT", 5, 1, 9) != 1 {
		t.Fatal("min bound")
	}
	t.Setenv("X_TEST_INT", "junk")
	if envInt("X_TEST_INT", 5, 1, 9) != 5 {
		t.Fatal("default")
	}
}
