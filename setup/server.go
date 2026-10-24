package setup

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kasuganosora/community/config"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
)

// InstallRequest is the wizard's final submission.
type InstallRequest struct {
	Locale   string          `json:"locale"`
	Mode     string          `json:"mode"` // fresh or misskey
	Domain   string          `json:"domain"`
	Database DatabaseParams  `json:"database"`
	Redis    RedisParams     `json:"redis"`
	Admin    AdminParams     `json:"admin"`
	Misskey  *MisskeyParams  `json:"misskey,omitempty"`
	Mastodon *MastodonParams `json:"mastodon,omitempty"`
}

// Server is the setup-mode HTTP server.
type Server struct {
	token       string
	t           *tracker
	mu          sync.Mutex
	running     bool
	failures    int
	lockedUntil time.Time
	frontendDir string
	// Restart replaces the process after a successful installation.
	Restart func()
}

// NewServer creates the setup server with a fresh one-time token and loads
// a previous (interrupted or failed) job, if any.
func NewServer(frontendDir string) *Server {
	s := &Server{token: randomSecret(18), t: &tracker{}, frontendDir: frontendDir, Restart: RestartProcess}
	if job, err := loadJob(); err == nil {
		for _, st := range job.Stages {
			if st.Status == StatusRunning {
				st.Status = StatusFailed
				st.Error = fail("interrupted")
				job.Status = StatusFailed
			}
		}
		if job.Status == StatusRunning {
			job.Status = StatusFailed
		}
		s.t.job = job
		s.t.persistLocked(true)
	}
	return s
}

// Token returns the one-time install token (printed to the logs).
func (s *Server) Token() string { return s.token }

// PrintToken writes the token to the container log in a prominent block.
func (s *Server) PrintToken() {
	banner := strings.Repeat("=", 64)
	msg := fmt.Sprintf("\n%s\n  Community setup mode\n  Open https://<your-domain>/setup and enter this install token:\n\n      %s\n\n  The token changes on every start and stops working after installation.\n%s\n", banner, s.token, banner)
	_, _ = fmt.Fprint(os.Stdout, msg)
	log.Info("Setup mode: install token printed to stdout; configuration file %s", config.EnvFile())
}

func (s *Server) checkToken(c *gin.Context) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().Before(s.lockedUntil) {
		c.JSON(http.StatusTooManyRequests, fail("rate_limited"))
		return false
	}
	got := c.GetHeader("X-Setup-Token")
	if got == "" {
		c.JSON(http.StatusUnauthorized, fail("token_required"))
		return false
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(got)), []byte(s.token)) != 1 {
		s.failures++
		if s.failures >= 10 {
			s.lockedUntil = time.Now().Add(time.Minute)
			s.failures = 0
		}
		c.JSON(http.StatusUnauthorized, fail("invalid_token"))
		return false
	}
	s.failures = 0
	return true
}

func respondErr(c *gin.Context, err error) {
	var se *Error
	if errors.As(err, &se) {
		c.JSON(http.StatusUnprocessableEntity, se)
		return
	}
	c.JSON(http.StatusUnprocessableEntity, fail("invalid_request"))
}

// Handler builds the gin engine for setup mode.
func (s *Server) Handler() *gin.Engine {
	app := gin.New()
	app.Use(gin.Recovery())
	app.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Next()
	})
	app.GET("/live", func(c *gin.Context) { c.JSON(200, gin.H{"status": "setup"}) })
	app.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "setup"}) })
	app.GET("/ready", func(c *gin.Context) { c.JSON(503, gin.H{"status": "setup"}) })
	api := app.Group("/api/setup")
	api.GET("/status", s.status)
	api.POST("/verify", func(c *gin.Context) {
		if s.checkToken(c) {
			c.JSON(200, gin.H{"ok": true})
		}
	})
	api.POST("/test/database", s.testDatabase)
	api.POST("/test/redis", s.testRedis)
	api.POST("/test/misskey", s.testMisskey)
	api.POST("/test/mastodon", s.testMastodon)
	api.POST("/test/object-storage", s.testObjectStorage)
	api.POST("/test/source-stopped", s.testSourceStopped)
	api.POST("/install", s.install)
	api.GET("/job", func(c *gin.Context) {
		if !s.checkToken(c) {
			return
		}
		job := s.t.snapshot()
		if job == nil {
			c.JSON(404, fail("no_job"))
			return
		}
		c.JSON(200, job)
	})
	api.POST("/job/retry", s.retry)

	if s.frontendDir != "" {
		app.Static("/assets", filepath.Join(s.frontendDir, "assets"))
		for _, f := range []string{"/logo.svg", "/favicon.ico", "/theme-init.js"} {
			p := filepath.Join(s.frontendDir, strings.TrimPrefix(f, "/"))
			if utils.IsFileExists(p) {
				app.StaticFile(f, p)
			}
		}
	}
	page := func(c *gin.Context) {
		p := filepath.Join(s.frontendDir, "setup.html")
		if s.frontendDir == "" || !utils.IsFileExists(p) {
			c.Data(200, "text/html; charset=utf-8", []byte("<!doctype html><title>Community setup</title><p>The frontend is not built. Use the /api/setup endpoints.</p>"))
			return
		}
		c.File(p)
	}
	app.GET("/setup", page)
	app.GET("/setup/*any", page)
	app.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(503, fail("setup_mode"))
			return
		}
		c.Redirect(http.StatusFound, "/setup")
	})
	return app
}

func (s *Server) status(c *gin.Context) {
	job := s.t.snapshot()
	resp := gin.H{
		"mode":      "setup",
		"locales":   SupportedLocales,
		"env_file":  config.EnvFile(),
		"resumable": setupPending(),
		"defaults": gin.H{
			"database_type": "postgres",
			"database_host": firstNonEmpty(os.Getenv("COMMUNITY_DEFAULT_DB_HOST"), "127.0.0.1"),
			"database_name": "community",
			"database_user": "community",
			"sqlite_path":   filepath.Join(defaultDataDir(), "community.db"),
			"redis_address": firstNonEmpty(os.Getenv("COMMUNITY_DEFAULT_REDIS_HOST"), "127.0.0.1") + ":6379",
		},
	}
	if job != nil {
		resp["job"] = gin.H{"status": job.Status, "mode": job.Mode, "locale": job.Locale}
	}
	if database, available := builtinDatabase(); available {
		// This endpoint is public. Do not return connection credentials.
		resp["builtin_database"] = gin.H{"type": database.Type}
	}
	c.JSON(200, resp)
}

func defaultDataDir() string {
	if info, err := os.Stat("/app/data"); err == nil && info.IsDir() {
		return "/app/data"
	}
	return filepath.Join(ConfigDir(), "data")
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func (s *Server) testDatabase(c *gin.Context) {
	if !s.checkToken(c) {
		return
	}
	var p DatabaseParams
	if c.ShouldBindJSON(&p) != nil {
		respondErr(c, fail("invalid_request"))
		return
	}
	if err := p.resolveTarget(); err != nil {
		respondErr(c, err)
		return
	}
	conn, err := openDatabase(p)
	if err != nil {
		respondErr(c, err)
		return
	}
	defer closeDB(conn)
	if databaseHasCommunityData(conn) {
		respondErr(c, fail("database_not_empty"))
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) testRedis(c *gin.Context) {
	if !s.checkToken(c) {
		return
	}
	var p RedisParams
	if c.ShouldBindJSON(&p) != nil {
		respondErr(c, fail("invalid_request"))
		return
	}
	if err := p.normalize(); err != nil {
		respondErr(c, err)
		return
	}
	if err := pingRedis(p); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

func (s *Server) testMisskey(c *gin.Context) {
	if !s.checkToken(c) {
		return
	}
	var p MisskeyParams
	if c.ShouldBindJSON(&p) != nil {
		respondErr(c, fail("invalid_request"))
		return
	}
	p.MediaSource = "local"
	p.FilesPath = "-"
	if err := p.normalize(); err != nil {
		respondErr(c, err)
		return
	}
	conn, err := openMisskey(p, p.Password)
	if err != nil {
		respondErr(c, err)
		return
	}
	defer closeDB(conn)
	info, ierr := inspectMisskey(conn)
	if ierr != nil {
		respondErr(c, ierr)
		return
	}
	c.JSON(200, gin.H{"ok": true, "inspection": info})
}

func (s *Server) testObjectStorage(c *gin.Context) {
	if !s.checkToken(c) {
		return
	}
	var req struct {
		Misskey MisskeyParams `json:"misskey"`
	}
	if c.ShouldBindJSON(&req) != nil {
		respondErr(c, fail("invalid_request"))
		return
	}
	p := req.Misskey
	o := p.ObjectStorage
	secret := o.SecretKey
	if secret == "" && p.Host != "" {
		if err := p.normalizeConnection(); err == nil {
			if conn, err := openMisskey(p, p.Password); err == nil {
				secret = misskeyObjectSecret(conn)
				closeDB(conn)
			}
		}
	}
	if strings.TrimSpace(o.Endpoint) == "" || strings.TrimSpace(o.Bucket) == "" || o.AccessKey == "" || secret == "" {
		respondErr(c, fail("object_storage_fields_required"))
		return
	}
	if err := checkObjectStorage(c.Request.Context(), o, secret); err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// normalizeConnection validates only the connection fields.
func (m *MisskeyParams) normalizeConnection() *Error {
	copyParams := *m
	copyParams.MediaSource, copyParams.FilesPath = "local", "-"
	if err := copyParams.normalize(); err != nil {
		return err
	}
	m.Host, m.Port, m.SSLMode = copyParams.Host, copyParams.Port, copyParams.SSLMode
	return nil
}

func (s *Server) install(c *gin.Context) {
	if !s.checkToken(c) {
		return
	}
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		respondErr(c, fail("already_running"))
		return
	}
	s.mu.Unlock()
	if existing := s.t.snapshot(); existing != nil {
		configDone := false
		for _, st := range existing.Stages {
			if st.Name == "config" && st.Status == StatusDone {
				configDone = true
			}
		}
		if configDone || existing.Status == "completed" {
			respondErr(c, fail("job_exists"))
			return
		}
	}
	if config.HasDatabaseConfig() {
		respondErr(c, fail("job_exists"))
		return
	}
	var req InstallRequest
	if c.ShouldBindJSON(&req) != nil {
		respondErr(c, fail("invalid_request"))
		return
	}
	job, secrets, err := prepareInstall(c.Request.Context(), &req)
	if err != nil {
		respondErr(c, err)
		return
	}
	if err := os.MkdirAll(ConfigDir(), 0o700); err != nil {
		respondErr(c, fail("config_write_failed", "the configuration directory is not writable"))
		return
	}
	if err := saveSecrets(secrets); err != nil {
		respondErr(c, fail("config_write_failed", "the configuration directory is not writable"))
		return
	}
	s.t.mu.Lock()
	s.t.job = job
	s.t.persistLocked(true)
	s.t.mu.Unlock()
	s.start(secrets)
	c.JSON(http.StatusAccepted, s.t.snapshot())
}

func (s *Server) retry(c *gin.Context) {
	if !s.checkToken(c) {
		return
	}
	job := s.t.snapshot()
	if job == nil || job.Status != StatusFailed {
		respondErr(c, fail("no_job"))
		return
	}
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if running {
		respondErr(c, fail("already_running"))
		return
	}
	secrets, err := loadSecrets()
	if err != nil {
		respondErr(c, fail("secrets_missing"))
		return
	}
	s.t.logf("info", "retrying from the first unfinished stage")
	s.start(secrets)
	c.JSON(http.StatusAccepted, s.t.snapshot())
}

func (s *Server) start(secrets *jobSecrets) {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	job := s.t.snapshot()
	stages := commonStages()
	switch job.Mode {
	case "misskey":
		stages = misskeyStages()
	case "mastodon":
		stages = mastodonStages()
	}
	r := &runner{t: s.t, secrets: secrets, stages: stages, onComplete: func() {
		go func() {
			time.Sleep(2 * time.Second)
			s.Restart()
		}()
	}}
	go func() {
		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()
		r.run(context.Background())
	}()
}

// prepareInstall validates the request and the target database before
// anything is written. Migration sources are not inspected here: a full
// source probe blocks the request on large databases, and the source_probe
// stage repeats the light check before configuration is written.
func prepareInstall(_ context.Context, req *InstallRequest) (*Job, *jobSecrets, error) {
	if !validLocale(req.Locale) {
		req.Locale = "en"
	}
	if req.Mode != "fresh" && req.Mode != "misskey" && req.Mode != "mastodon" {
		return nil, nil, fail("invalid_request")
	}
	if err := req.Database.resolveTarget(); err != nil {
		return nil, nil, err
	}
	if err := req.Redis.normalize(); err != nil {
		return nil, nil, err
	}
	if err := req.Admin.normalize(); err != nil {
		return nil, nil, err
	}
	conn, err := openDatabase(req.Database)
	if err != nil {
		return nil, nil, err
	}
	hasData := databaseHasCommunityData(conn)
	closeDB(conn)
	if hasData {
		return nil, nil, fail("database_not_empty")
	}
	if err := pingRedis(req.Redis); err != nil {
		return nil, nil, err
	}
	secrets := &jobSecrets{DatabasePassword: req.Database.Password, RedisPassword: req.Redis.Password}
	baseURL := ""
	if req.Mode == "misskey" {
		if req.Misskey == nil {
			return nil, nil, fail("misskey_fields_required")
		}
		m := req.Misskey
		if err := m.normalize(); err != nil {
			return nil, nil, err
		}
		if m.MediaSource == "local" {
			if info, err := os.Stat(m.FilesPath); err != nil || !info.IsDir() {
				return nil, nil, fail("misskey_files_missing")
			}
		}
		if m.IDMethod == "" {
			return nil, nil, fail("invalid_id_method")
		}
		b, derr := normalizeBaseURL(req.Domain)
		if derr != nil {
			return nil, nil, derr
		}
		baseURL = b
		secrets.MisskeyPassword = m.Password
		secrets.S3SecretKey = m.ObjectStorage.SecretKey
		secrets.MisskeyRedisPassword = m.StopCheck.RedisPassword
		m.Password = ""
		m.ObjectStorage.SecretKey = ""
		m.StopCheck.RedisPassword = ""
	} else if req.Mode == "mastodon" {
		if req.Mastodon == nil {
			return nil, nil, fail("mastodon_fields_required")
		}
		m := req.Mastodon
		if err := m.normalize(); err != nil {
			return nil, nil, err
		}
		if m.MediaSource == "local" {
			if info, err := os.Stat(m.FilesPath); err != nil || !info.IsDir() {
				return nil, nil, fail("mastodon_files_missing")
			}
		}
		b, derr := normalizeBaseURL(req.Domain)
		if derr != nil {
			return nil, nil, derr
		}
		baseURL = b
		secrets.MisskeyPassword = m.Password
		secrets.S3SecretKey = m.ObjectStorage.SecretKey
		m.Password = ""
		m.ObjectStorage.SecretKey = ""
	} else {
		b, derr := normalizeBaseURL(req.Domain)
		if derr != nil {
			return nil, nil, derr
		}
		baseURL = b
	}
	salt := utils.GetRandomString(64)
	hash, err := models.EncryptPassword(req.Admin.Password, salt)
	if err != nil {
		return nil, nil, fail("weak_password")
	}
	secrets.AdminSalt, secrets.AdminHash = salt, hash

	job := newJob(req.Mode)
	job.Locale = req.Locale
	job.BaseURL = baseURL
	job.Database = req.Database.Type
	job.Admin = req.Admin.Username
	job.Email = req.Admin.Email
	job.Misskey = req.Misskey
	job.Mastodon = req.Mastodon
	target := req.Database
	target.Password = ""
	job.Target = target
	rp := req.Redis
	rp.Password = ""
	job.Redis = rp
	return job, secrets, nil
}

// RestartProcess re-executes the binary so it boots with the new
// configuration. If exec fails the process exits and the container's
// restart policy starts it again.
func RestartProcess() {
	log.Info("Restarting to apply the new configuration")
	exe, err := os.Executable()
	if err == nil {
		err = syscall.Exec(exe, os.Args, os.Environ())
	}
	log.Error("Re-exec failed (%v); exiting so the supervisor restarts the server", err)
	os.Exit(0)
}

// Run serves setup mode until the process is replaced.
func Run(listen, frontendDir string) error {
	s := NewServer(frontendDir)
	s.PrintToken()
	gin.SetMode(gin.ReleaseMode)
	srv := &http.Server{Addr: listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	log.Info("Setup wizard listening on %s", listen)
	return srv.ListenAndServe()
}

// sourceBaseURL picks the domain of a migration. It is the source's:
// identities (actor and object URIs, keys) are preserved, so it cannot be
// chosen freely. When it was detected the input must match (or be empty);
// otherwise the input is required and must match a candidate if any.
func sourceBaseURL(input, detected string, candidates []string) (string, *Error) {
	if detected != "" {
		if input != "" {
			if given, derr := normalizeBaseURL(input); derr != nil || given != detected {
				return "", fail("domain_mismatch", detected)
			}
		}
		return detected, nil
	}
	b, derr := normalizeBaseURL(input)
	if derr != nil {
		return "", derr
	}
	if len(candidates) > 0 && !slices.Contains(candidates, b) {
		return "", fail("domain_mismatch", strings.Join(candidates, ", "))
	}
	return b, nil
}

func (s *Server) testMastodon(c *gin.Context) {
	if !s.checkToken(c) {
		return
	}
	var p MastodonParams
	if c.ShouldBindJSON(&p) != nil {
		respondErr(c, fail("invalid_request"))
		return
	}
	if err := p.normalizeConnection(); err != nil {
		respondErr(c, err)
		return
	}
	conn, err := openMastodon(p, p.Password)
	if err != nil {
		respondErr(c, err)
		return
	}
	defer closeDB(conn)
	info, ierr := inspectMastodon(conn)
	if ierr != nil {
		respondErr(c, ierr)
		return
	}
	c.JSON(200, gin.H{"ok": true, "inspection": info})
}
