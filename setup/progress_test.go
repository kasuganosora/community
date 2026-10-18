package setup

import (
	"testing"
	"time"
)

func TestStageProgressRateAndETA(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COMMUNITY_CONFIG_DIR", dir)
	tr := &tracker{job: newJob("misskey"), lastWrite: time.Now()}
	tr.setProgress("misskey_import", "import/posts", 0, 1000)
	p := tr.stage("misskey_import").Progress
	if p == nil || p.ETASeconds != -1 || p.Total != 1000 {
		t.Fatalf("initial progress %+v", p)
	}
	p.started = time.Now().Add(-10 * time.Second) // 10s into the phase
	tr.setProgress("misskey_import", "import/posts", 500, 1000)
	p = tr.stage("misskey_import").Progress
	if p.Rate < 45 || p.Rate > 55 || p.ETASeconds < 9 || p.ETASeconds > 11 {
		t.Fatalf("rate/eta %+v", p)
	}
	tr.setProgress("misskey_import", "import/relations", 10, 20)
	if p = tr.stage("misskey_import").Progress; p.Phase != "import/relations" || p.Rate != 0 {
		t.Fatalf("new phase must reset speed: %+v", p)
	}
	tr.setProgress("misskey_import", "import/relations", 20, 20)
	if p = tr.stage("misskey_import").Progress; p.ETASeconds != 0 {
		t.Fatalf("finished phase ETA %+v", p)
	}
	tr.heartbeat()
	snap := tr.snapshot()
	if snap.HeartbeatAt == nil || snap.ServerTime.IsZero() {
		t.Fatal("heartbeat/server time missing from snapshot")
	}
}
