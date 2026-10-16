package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/log"
)

// Stage statuses.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// Stage is one resumable step of an installation job.
type Stage struct {
	Name       string           `json:"name"`
	Status     string           `json:"status"`
	Counts     map[string]int64 `json:"counts,omitempty"`
	Error      *Error           `json:"error,omitempty"`
	StartedAt  *time.Time       `json:"started_at,omitempty"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
}

// LogLine is one entry of the job's live log. Messages never contain
// credentials.
type LogLine struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// Job is the persisted installation state (setup-state.json in the config
// directory). It holds no secrets; those live in setup-secrets.json (0600)
// until the installation completes.
type Job struct {
	ID        string    `json:"id"`
	Mode      string    `json:"mode"` // fresh, misskey or mastodon
	Status    string    `json:"status"`
	Locale    string    `json:"locale"`
	BaseURL   string    `json:"base_url"`
	Database  string    `json:"database"`
	Admin     string    `json:"admin"`
	Email     string    `json:"admin_email"`
	Stages    []*Stage  `json:"stages"`
	Log       []LogLine `json:"log"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	Misskey  *MisskeyParams  `json:"misskey,omitempty"`  // passwords stripped
	Mastodon *MastodonParams `json:"mastodon,omitempty"` // passwords stripped
	// Database parameters without the password, used to rebuild .env on a
	// retry before the config stage finished.
	Target DatabaseParams `json:"target"`
	Redis  RedisParams    `json:"redis"`
}

// jobSecrets are needed only while a job runs and are deleted afterwards.
type jobSecrets struct {
	DatabasePassword string `json:"database_password,omitempty"`
	RedisPassword    string `json:"redis_password,omitempty"`
	AdminSalt        string `json:"admin_salt"`
	AdminHash        string `json:"admin_hash"`
	MisskeyPassword  string `json:"misskey_password,omitempty"` // source DB password (Misskey or Mastodon)
	S3SecretKey      string `json:"s3_secret_key,omitempty"`
}

const maxLogLines = 500

func loadJob() (*Job, error) {
	data, err := os.ReadFile(configPath(stateName))
	if err != nil {
		return nil, err
	}
	var j Job
	if err = json.Unmarshal(data, &j); err != nil {
		return nil, err
	}
	return &j, nil
}

func loadSecrets() (*jobSecrets, error) {
	data, err := os.ReadFile(configPath(secretsName))
	if err != nil {
		return nil, err
	}
	var s jobSecrets
	if err = json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func saveSecrets(s *jobSecrets) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeFileAtomic(configPath(secretsName), data, 0o600)
}

// tracker serializes access to the job and persists every change.
type tracker struct {
	mu        sync.Mutex
	job       *Job
	lastWrite time.Time
}

func (t *tracker) snapshot() *Job {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job == nil {
		return nil
	}
	data, _ := json.Marshal(t.job)
	var copyJob Job
	_ = json.Unmarshal(data, &copyJob)
	return &copyJob
}

func (t *tracker) persistLocked(force bool) {
	if t.job == nil {
		return
	}
	t.job.UpdatedAt = time.Now().UTC()
	if !force && time.Since(t.lastWrite) < 400*time.Millisecond {
		return
	}
	data, err := json.MarshalIndent(t.job, "", "  ")
	if err != nil {
		return
	}
	if err = writeFileAtomic(configPath(stateName), data, 0o600); err != nil {
		log.Warn("setup: cannot persist job state: %v", err)
		return
	}
	t.lastWrite = time.Now()
}

func (t *tracker) logf(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	switch level {
	case "error":
		log.Error("setup: %s", msg)
	case "warn":
		log.Warn("setup: %s", msg)
	default:
		log.Info("setup: %s", msg)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.job == nil {
		return
	}
	t.job.Log = append(t.job.Log, LogLine{Time: time.Now().UTC(), Level: level, Message: msg})
	if len(t.job.Log) > maxLogLines {
		t.job.Log = t.job.Log[len(t.job.Log)-maxLogLines:]
	}
	t.persistLocked(level != "info")
}

func (t *tracker) stage(name string) *Stage {
	for _, s := range t.job.Stages {
		if s.Name == name {
			return s
		}
	}
	return nil
}

func (t *tracker) setCount(stage, key string, value int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := t.stage(stage); s != nil {
		if s.Counts == nil {
			s.Counts = map[string]int64{}
		}
		s.Counts[key] = value
	}
	t.persistLocked(false)
}

func (t *tracker) setCounts(stage string, counts map[string]int64) {
	for k, v := range counts {
		t.setCount(stage, k, v)
	}
}

func (t *tracker) update(fn func(j *Job)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	fn(t.job)
	t.persistLocked(true)
}

// stageFunc runs one stage. Returning an *Error marks the stage failed with
// that code; any other error is reported as stage_failed.
type stageFunc func(ctx context.Context, r *runner) error

type runner struct {
	t       *tracker
	secrets *jobSecrets
	stages  map[string]stageFunc
	// onComplete runs after the job finished successfully (restart).
	onComplete func()
}

func stageNames(mode string) []string {
	if mode == "mastodon" {
		return []string{"config", "schema", "mastodon_media", "mastodon_export", "mastodon_preflight", "mastodon_import", "admin", "finalize"}
	}
	if mode == "misskey" {
		return []string{"config", "schema", "misskey_meta", "misskey_media", "misskey_export", "misskey_preflight", "misskey_import", "passkeys", "admin", "finalize"}
	}
	return []string{"config", "schema", "admin", "finalize"}
}

func newJob(mode string) *Job {
	now := time.Now().UTC()
	j := &Job{ID: randomSecret(9), Mode: mode, Status: StatusRunning, CreatedAt: now, UpdatedAt: now}
	for _, name := range stageNames(mode) {
		j.Stages = append(j.Stages, &Stage{Name: name, Status: StatusPending})
	}
	return j
}

// run executes every stage that is not done yet, in order, stopping at the
// first failure. Completed stages are checkpoints: a retry resumes after
// them, and stages are written to be idempotent themselves.
func (r *runner) run(ctx context.Context) {
	r.t.update(func(j *Job) { j.Status = StatusRunning })
	names := make([]string, 0)
	r.t.mu.Lock()
	for _, s := range r.t.job.Stages {
		names = append(names, s.Name)
	}
	r.t.mu.Unlock()
	for _, name := range names {
		var current Stage
		r.t.mu.Lock()
		s := r.t.stage(name)
		current = *s
		r.t.mu.Unlock()
		if current.Status == StatusDone || current.Status == StatusSkipped {
			continue
		}
		fn := r.stages[name]
		if fn == nil {
			r.failStage(name, fail("stage_failed", "unknown stage"))
			return
		}
		now := time.Now().UTC()
		r.t.update(func(j *Job) {
			st := r.t.stage(name)
			st.Status, st.Error, st.StartedAt, st.FinishedAt = StatusRunning, nil, &now, nil
		})
		r.t.logf("info", "stage %s started", name)
		err := runStage(ctx, fn, r)
		if errors.Is(err, errSkipStage) {
			done := time.Now().UTC()
			r.t.update(func(j *Job) {
				st := r.t.stage(name)
				st.Status, st.FinishedAt = StatusSkipped, &done
			})
			r.t.logf("info", "stage %s skipped", name)
			continue
		}
		if err != nil {
			var se *Error
			if !errors.As(err, &se) {
				se = fail("stage_failed")
				r.t.logf("error", "stage %s failed: %s", name, sanitize(err.Error()))
			} else {
				r.t.logf("error", "stage %s failed: %s", name, se.Error())
			}
			r.failStage(name, se)
			return
		}
		done := time.Now().UTC()
		r.t.update(func(j *Job) {
			st := r.t.stage(name)
			st.Status, st.FinishedAt = StatusDone, &done
		})
		r.t.logf("info", "stage %s finished", name)
	}
	r.t.update(func(j *Job) { j.Status = "completed" })
	if r.onComplete != nil {
		r.onComplete()
	}
}

// runStage converts a panic in a stage into a failure instead of killing
// the setup server.
func runStage(ctx context.Context, fn stageFunc, r *runner) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return fn(ctx, r)
}

var errSkipStage = errors.New("skip stage")

func (r *runner) failStage(name string, e *Error) {
	done := time.Now().UTC()
	r.t.update(func(j *Job) {
		if st := r.t.stage(name); st != nil {
			st.Status, st.Error, st.FinishedAt = StatusFailed, e, &done
		}
		j.Status = StatusFailed
	})
}

// sanitize keeps a log message short and drops anything that looks like a
// connection string or key=value secret.
func sanitize(msg string) string {
	fields := strings.Fields(msg)
	for i, f := range fields {
		lower := strings.ToLower(f)
		if strings.Contains(lower, "password") || strings.Contains(lower, "://") || strings.Contains(lower, "secret") || strings.Contains(f, "@") {
			fields[i] = "[redacted]"
		}
	}
	out := strings.Join(fields, " ")
	if len(out) > 300 {
		out = out[:300] + "…"
	}
	return out
}
