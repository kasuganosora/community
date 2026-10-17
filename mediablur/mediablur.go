// Package mediablur fills in BlurHash placeholders (and dimensions) of
// federated image attachments whose origin did not send one, so sensitive
// media can be shown as a placeholder without loading the real file.
package mediablur

import (
	"context"
	"github.com/kasuganosora/community/db/writebuffer"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kasuganosora/community/activitypub"
	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/media"
	"github.com/kasuganosora/community/models"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

// MaxImageBytes bounds the downloaded file; each file also has a time cap.
const (
	MaxImageBytes = 8 << 20
	fileTimeout   = 20 * time.Second
)

// Service computes blurhashes. NewService wires production dependencies.
type Service struct {
	DB    func() *gorm.DB
	Fetch func(ctx context.Context, rawURL string, limit int64) ([]byte, string, error)
}

// NewService uses the SSRF-protected media fetcher.
func NewService() *Service {
	return &Service{DB: db.GetDB, Fetch: activitypub.FetchRemoteMedia}
}

// One computes and stores the blurhash (and missing size) of an attachment.
// It returns whether a hash was stored. Unfetchable or undecodable files
// are simply left without one.
func (s *Service) One(ctx context.Context, att *models.MediaAttachment) bool {
	if att.RemoteUrl == "" || att.Type != models.MediaTypeImage || att.Blurhash != "" {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, fileTimeout)
	defer cancel()
	data, _, err := s.Fetch(ctx, att.RemoteUrl, MaxImageBytes)
	if err != nil {
		return false
	}
	hash, w, h, err := media.BlurhashFromBytes(data)
	if err != nil || hash == "" {
		return false
	}
	updates := map[string]any{"blurhash": hash}
	if att.Width == nil || att.Height == nil || *att.Width == 0 || *att.Height == 0 {
		updates["width"], updates["height"] = w, h
	}

	// Another worker may have stored the hash meanwhile; never rewrite it.
	// Federation-tier data: batched through the write buffer.
	return writebuffer.Update(s.DB().WithContext(ctx), "media_attachments", att.Id, updates, "(blurhash = '' OR blurhash IS NULL)") == nil
}

// Status handles every blurhash-less remote image of one status.
func (s *Service) Status(ctx context.Context, statusID uint64) int {
	var atts []*models.MediaAttachment
	if err := s.DB().WithContext(ctx).Where("status_id = ? AND remote_url <> '' AND type = ? AND (blurhash = '' OR blurhash IS NULL)",
		statusID, models.MediaTypeImage).Find(&atts).Error; err != nil {
		return 0
	}
	n := 0
	for _, a := range atts {
		if s.One(ctx, a) {
			n++
		}
	}

	return n
}

// Stats summarizes a Backfill.
type Stats struct{ Candidates, Done, Failed int64 }

// Backfill generates blurhashes for remote images of the last days days
// (0 = any age), optionally only for sensitive statuses/files.
func (s *Service) Backfill(ctx context.Context, days, limit, concurrency int, perSecond float64, sensitiveOnly bool, progress func(id uint64, ok bool)) (Stats, error) {
	var st Stats
	q := s.DB().WithContext(ctx).Model(&models.MediaAttachment{}).
		Joins("JOIN statuses ON statuses.id = media_attachments.status_id").
		Where("media_attachments.remote_url <> '' AND media_attachments.type = ? AND (media_attachments.blurhash = '' OR media_attachments.blurhash IS NULL) AND statuses.deleted_at IS NULL", models.MediaTypeImage).
		Order("media_attachments.id DESC")
	if sensitiveOnly {
		q = q.Where("(statuses.sensitive = ? OR media_attachments.sensitive = ?)", true, true)
	}
	if days > 0 {
		q = q.Where("statuses.created_at >= ?", time.Now().AddDate(0, 0, -days))
	}
	if limit > 0 {
		q = q.Limit(limit)
	}
	var atts []*models.MediaAttachment
	if err := q.Select("media_attachments.*").Find(&atts).Error; err != nil {
		return st, err
	}
	st.Candidates = int64(len(atts))
	if concurrency < 1 {
		concurrency = 1
	}
	var lim *rate.Limiter
	if perSecond > 0 {
		lim = rate.NewLimiter(rate.Limit(perSecond), 1)
	}
	jobs := make(chan *models.MediaAttachment)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for a := range jobs {
				ok := s.One(ctx, a)
				if ok {
					atomic.AddInt64(&st.Done, 1)
				} else {
					atomic.AddInt64(&st.Failed, 1)
				}
				if progress != nil {
					mu.Lock()
					progress(a.Id, ok)
					mu.Unlock()
				}
			}
		}()
	}
loop:
	for _, a := range atts {
		if lim != nil && lim.Wait(ctx) != nil {
			break
		}
		select {
		case jobs <- a:
		case <-ctx.Done():
			break loop
		}
	}
	close(jobs)
	wg.Wait()

	return st, ctx.Err()
}

// ---- background queue ----

var (
	qMu  sync.Mutex
	jobs chan uint64
)

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}

	return def
}

// Enabled: BLURHASH_BACKGROUND=false turns the background job off.
func Enabled() bool {
	v := strings.ToLower(os.Getenv("BLURHASH_BACKGROUND"))

	return v != "false" && v != "0" && v != "off"
}

// Enqueue schedules a status; it never blocks and drops when the queue is full.
func Enqueue(statusID uint64) bool {
	if !Enabled() || statusID == 0 {
		return false
	}
	qMu.Lock()
	if jobs == nil {
		jobs = startQueue(NewService(), envInt("BLURHASH_WORKERS", 2), envInt("BLURHASH_QUEUE", 256), rate.Limit(envInt("BLURHASH_RATE_PER_SEC", 5)))
	}
	ch := jobs
	qMu.Unlock()
	select {
	case ch <- statusID:
		return true
	default:
		return false
	}
}

func startQueue(svc *Service, workers, size int, limit rate.Limit) chan uint64 {
	ch := make(chan uint64, size)
	lim := rate.NewLimiter(limit, 1)
	for i := 0; i < workers; i++ {
		go func() {
			for id := range ch {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				if lim.Wait(ctx) == nil {
					svc.Status(ctx, id)
				}
				cancel()
			}
		}()
	}

	return ch
}

func init() {
	event.Register("status", "*", &event.Handler{Exec: func(e event.Event) bool {
		st, ok := e.Data.(*models.Status)
		if !ok || st == nil || (e.Name != "after_create" && e.Name != "after_update") || st.OriginUri == nil {
			return true
		}
		id := st.Id
		// The event fires inside the creating transaction.
		time.AfterFunc(3*time.Second, func() {
			if !Enqueue(id) && Enabled() {
				log.Printf("mediablur: queue full or disabled, status %d left for backfill", id)
			}
		})

		return true
	}})
}
