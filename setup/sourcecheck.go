package setup

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/datatransfer/sourcecheck"
	"github.com/redis/go-redis/v9"
)

// StopCheckParams configures the "is Misskey stopped" check that gates the
// final read (stage misskey_export). WebURL must be the LOCAL/internal
// Misskey address (default http://127.0.0.1:3000/api/meta or a compose
// service such as http://web:3000/api/meta), never the public domain.
type StopCheckParams struct {
	WebURL        string `json:"web_url"`
	RedisAddress  string `json:"redis_address"` // empty = queue check skipped
	RedisPassword string `json:"redis_password,omitempty"`
	RedisDB       int    `json:"redis_db"`
	RedisPrefix   string `json:"redis_prefix"`
	// Force runs the final read even if the check says RUNNING (same as
	// community-transfer --force); every use is logged as WARN and recorded.
	Force bool `json:"force"`
}

// sourceCheckInterval is the write-sample interval; tests shorten it.
var sourceCheckInterval = sourcecheck.DefaultInterval

func runStopCheck(ctx context.Context, m MisskeyParams, password, redisPassword string) *sourcecheck.Result {
	dsn := postgresKeywordDSN(m.Host, m.Port, m.User, password, m.Name, m.SSLMode)
	db, err := sourcecheck.OpenPG(dsn)
	var src sourcecheck.Source
	if err == nil {
		defer db.Close()
		src = sourcecheck.NewPGSource(db, "note")
	}
	var rc redis.UniversalClient
	if a := strings.TrimSpace(m.StopCheck.RedisAddress); a != "" {
		rc = redis.NewClient(&redis.Options{Addr: a, Password: redisPassword, DB: m.StopCheck.RedisDB, DialTimeout: 3 * time.Second})
		defer rc.Close()
	}
	return sourcecheck.Run(ctx, sourcecheck.Config{Source: src, Interval: sourceCheckInterval, WebURL: strings.TrimSpace(m.StopCheck.WebURL), Redis: rc, Prefix: m.StopCheck.RedisPrefix})
}

// testSourceStopped runs the check for the review step's panel.
func (s *Server) testSourceStopped(c *gin.Context) {
	if !s.checkToken(c) {
		return
	}
	var p MisskeyParams
	if c.ShouldBindJSON(&p) != nil {
		respondErr(c, fail("invalid_request"))
		return
	}
	p.MediaSource, p.FilesPath = "local", "-"
	if err := p.normalize(); err != nil {
		respondErr(c, err)
		return
	}
	res := runStopCheck(c.Request.Context(), p, p.Password, p.StopCheck.RedisPassword)
	c.JSON(200, gin.H{"result": res, "interval_seconds": int(sourceCheckInterval / time.Second)})
}

// gateSourceStopped is the server-side gate before the final read.
func (r *runner) gateSourceStopped(ctx context.Context, m MisskeyParams) error {
	r.t.logf("info", "checking that Misskey is stopped (two samples %s apart)…", sourceCheckInterval)
	res := runStopCheck(ctx, m, r.secrets.MisskeyPassword, r.secrets.MisskeyRedisPassword)
	r.t.update(func(j *Job) { j.SourceCheck = res })
	for _, ch := range res.Checks {
		level := "info"
		if ch.Status == sourcecheck.Fail {
			level = "error"
		}
		r.t.logf(level, "[%s] %s: %s", strings.ToUpper(ch.Status), ch.Title, sanitize(strings.Join(ch.Reasons, "; ")))
	}
	if res.Status == sourcecheck.Stopped {
		return nil
	}
	if !m.StopCheck.Force {
		return fail("source_running", strings.Join(res.Failed(), ", "))
	}
	o := sourcecheck.NewOverride(res, "setup-wizard", "wizard")
	r.t.logf("warn", "%s", o.LogLine())
	r.t.update(func(j *Job) { j.SourceCheckOverrides = append(j.SourceCheckOverrides, o) })
	return nil
}
