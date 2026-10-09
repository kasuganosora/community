package linkcard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/storage"
	"github.com/kasuganosora/community/systemsettings"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"log"
)

// Outcome says what Process did with a status.
type Outcome int

const (
	Skipped Outcome = iota // not eligible / no link
	Created                // a card was stored
	Reused                 // card copied from an earlier status with the same URL
	Failed                 // the link could not be previewed
)

const (
	// MaxImageBytes bounds the stored preview image.
	MaxImageBytes = 2 << 20
	// pageTimeout is the whole-request budget for the page fetch.
	pageTimeout  = 5 * time.Second
	maxRedirects = 3
	failureTTL   = time.Hour
	reuseWindow  = 30 * 24 * time.Hour
	cardImageDir = "cards/"
)

// Service creates cards. The zero value is not usable; use NewService.
type Service struct {
	DB         func() *gorm.DB
	Fetcher    *Fetcher
	FetchImage func(ctx context.Context, rawURL string, limit int64) ([]byte, string, error)
	SaveImage  func(key string, data []byte) error
	BaseURL    func() string

	mu     sync.Mutex
	failed map[string]time.Time
}

// NewService wires the production dependencies.
func NewService() *Service {
	return &Service{
		DB:         db.GetDB,
		Fetcher:    &Fetcher{Client: activitypub.NewSafeHTTPClient(pageTimeout, maxRedirects), Validate: activitypub.ValidateURI},
		FetchImage: activitypub.FetchRemoteMedia,
		SaveImage: func(key string, data []byte) error {
			_, err := storage.Save(key, data)
			return err
		},
		BaseURL: systemsettings.GetBaseURL,
		failed:  map[string]time.Time{},
	}
}

func urlHash(u string) string {
	sum := sha256.Sum256([]byte(u))
	return hex.EncodeToString(sum[:])
}

func (s *Service) recentlyFailed(hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed == nil {
		s.failed = map[string]time.Time{}
	}
	t, ok := s.failed[hash]
	if ok && time.Since(t) < failureTTL {
		return true
	}
	delete(s.failed, hash)
	return false
}

func (s *Service) markFailed(hash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed == nil {
		s.failed = map[string]time.Time{}
	}
	if len(s.failed) > 10000 {
		s.failed = map[string]time.Time{}
	}
	s.failed[hash] = time.Now()
}

type statusRow struct {
	models.Status
	Content string
}

// Process builds the card of one status. It never returns an error for an
// unpreviewable link; the Outcome says what happened. Errors are DB errors.
func (s *Service) Process(ctx context.Context, statusID uint64) (Outcome, error) {
	conn := s.DB().WithContext(ctx)
	var st models.Status
	if err := conn.Select("id", "domain", "reblog_id", "visibility", "deleted_at").
		Where("deleted_at IS NULL").First(&st, statusID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Skipped, nil
		}
		return Skipped, err
	}
	if st.ReblogId != nil && *st.ReblogId > 0 || st.Visibility == models.StatusVisibilityDirect {
		return Skipped, nil
	}
	if st.Domain != nil && *st.Domain != "" {
		blocked, err := domainSuspended(conn, *st.Domain)
		if err != nil || blocked {
			return Skipped, err
		}
	}
	var media int64
	if err := conn.Model(&models.MediaAttachment{}).Where("status_id = ?", statusID).Count(&media).Error; err != nil {
		return Skipped, err
	}
	if media > 0 {
		return Skipped, nil
	}
	var content models.StatusContent
	if err := conn.Select("status_id", "content").First(&content, statusID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Skipped, nil
		}
		return Skipped, err
	}
	link := FirstLink(content.Content)
	if link == "" {
		return Skipped, nil
	}
	hash := urlHash(link)

	var existing models.StatusCard
	err := conn.Where("status_id = ?", statusID).First(&existing).Error
	if err == nil && existing.UrlHash == hash {
		return Skipped, s.cacheOnContent(conn, statusID, &existing)
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return Skipped, err
	}

	// De-dupe by URL: reuse a recent card (and its stored image).
	var donor models.StatusCard
	err = conn.Where("url_hash = ? AND created_at > ?", hash, time.Now().Add(-reuseWindow)).Order("id DESC").First(&donor).Error
	if err == nil {
		card := donor
		card.Id, card.StatusId, card.CreatedAt = 0, statusID, nil
		return Reused, s.store(conn, &card)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Skipped, err
	}

	if s.recentlyFailed(hash) {
		return Failed, nil
	}
	fctx, cancel := context.WithTimeout(ctx, pageTimeout*2)
	defer cancel()
	meta, ferr := s.Fetcher.FetchMeta(fctx, link)
	if ferr != nil {
		s.markFailed(hash)
		return Failed, nil
	}
	card := &models.StatusCard{
		StatusId: statusID, Url: link, UrlHash: hash, Title: meta.Title, Description: meta.Description,
		CardType: meta.Type, AuthorName: meta.AuthorName, AuthorUrl: meta.AuthorURL,
		ProviderName: meta.ProviderName, ProviderUrl: meta.ProviderURL,
	}
	if meta.Image != "" {
		card.ImageUrl = s.storeImage(fctx, meta.Image)
	}
	return Created, s.store(conn, card)
}

func domainSuspended(conn *gorm.DB, domain string) (bool, error) {
	suspend, _, err := models.DomainSeverities(conn)
	if err != nil {
		return false, err
	}
	d := strings.ToLower(domain)
	for {
		if suspend[d] {
			return true, nil
		}
		i := strings.IndexByte(d, '.')
		if i < 0 {
			return false, nil
		}
		d = d[i+1:]
	}
}

var imageExt = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}

// storeImage downloads the preview image through the SSRF-safe media
// fetcher and keeps it in the storage layer (local disk or S3/R2). On any
// problem the card simply has no image; remote images are never hotlinked.
func (s *Service) storeImage(ctx context.Context, rawURL string) string {
	data, _, err := s.FetchImage(ctx, rawURL, MaxImageBytes)
	if err != nil || len(data) == 0 {
		return ""
	}
	ext, ok := imageExt[http.DetectContentType(data)]
	if !ok {
		return ""
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:16]) + ext
	if err := s.SaveImage(cardImageDir+name, data); err != nil {
		log.Printf("linkcard: saving image: %v", err)
		return ""
	}
	return strings.TrimRight(s.BaseURL(), "/") + "/card-images/" + name
}

func (s *Service) store(conn *gorm.DB, card *models.StatusCard) error {
	err := conn.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "status_id"}},
		UpdateAll: true,
	}).Create(card).Error
	if err != nil {
		return err
	}
	return s.cacheOnContent(conn, card.StatusId, card)
}

// cacheOnContent mirrors the card into status_contents.card, where the
// status serializer already reads it. UpdateColumn skips the content
// hooks (tag/mention re-binding).
func (s *Service) cacheOnContent(conn *gorm.DB, statusID uint64, card *models.StatusCard) error {
	// The column has a JSON serializer; hand it the map itself.
	return conn.Session(&gorm.Session{SkipHooks: true}).Model(&models.StatusContent{}).Where("status_id = ?", statusID).
		Select("card").Updates(&models.StatusContent{Card: card.APIMap()}).Error
}

// ---- background queue ----

type queue struct {
	svc     *Service
	jobs    chan uint64
	limiter *rate.Limiter
}

var (
	qMu sync.Mutex
	q   *queue
)

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// Enabled reports whether background link previews are on
// (LINK_CARDS_ENABLED=false turns them off).
func Enabled() bool {
	v := strings.ToLower(os.Getenv("LINK_CARDS_ENABLED"))
	return v != "false" && v != "0" && v != "off"
}

// Enqueue schedules a status for card creation. It never blocks: when the
// queue is full the job is dropped (tootctl link-cards backfill catches up).
func Enqueue(statusID uint64) bool {
	if !Enabled() || statusID == 0 {
		return false
	}
	qMu.Lock()
	if q == nil {
		q = startQueue(NewService(), envInt("LINK_CARD_WORKERS", 2), envInt("LINK_CARD_QUEUE", 256),
			rate.Limit(envInt("LINK_CARD_RATE_PER_SEC", 5)))
	}
	cur := q
	qMu.Unlock()
	select {
	case cur.jobs <- statusID:
		return true
	default:
		return false
	}
}

func startQueue(svc *Service, workers, size int, limit rate.Limit) *queue {
	cur := &queue{svc: svc, jobs: make(chan uint64, size), limiter: rate.NewLimiter(limit, 1)}
	for i := 0; i < workers; i++ {
		go func() {
			for id := range cur.jobs {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				if err := cur.limiter.Wait(ctx); err == nil {
					if _, err := cur.svc.Process(ctx, id); err != nil {
						log.Printf("linkcard: status %d: %v", id, err)
					}
				}
				cancel()
			}
		}()
	}
	return cur
}

func init() {
	event.Register("status", "*", &event.Handler{Exec: func(e event.Event) bool {
		st, ok := e.Data.(*models.Status)
		if !ok || st == nil || (e.Name != "after_create" && e.Name != "after_update") {
			return true
		}
		id := st.Id
		// The event fires inside the creating transaction; give it (and the
		// attachment linking) a moment to commit before looking at the rows.
		time.AfterFunc(3*time.Second, func() { Enqueue(id) })
		return true
	}})
}
