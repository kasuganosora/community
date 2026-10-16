package activitypub

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// Misskey does not federate avatar decorations: they are absent from its
// ActivityPub actor and Misskey only renders decorations of its own users.
// For accounts on Misskey-family servers we therefore read them from the
// origin's public API (POST /api/users/show), the same way remote reaction
// counts are read, and keep a copy on the account row. Images are served to
// clients through the media proxy only.

const (
	remoteDecorationRefreshTTL  = 6 * time.Hour
	remoteDecorationForceMinAge = time.Minute
	remoteDecorationTimeout     = 10 * time.Second
	remoteDecorationWorkers     = 2
	remoteDecorationMaxURL      = 2000
)

type remoteDecorationJob struct {
	accountID uint64
	force     bool
}

var remoteDecorations struct {
	once    sync.Once
	queue   chan remoteDecorationJob
	limiter *hostLimiter
}

// scheduleRemoteAvatarDecorations queues a best-effort decoration refresh for
// a remote account. force (an Update{Person} arrived) bypasses the 6h TTL but
// not the one-minute floor. Excess jobs are dropped, as with collection totals.
func scheduleRemoteAvatarDecorations(accountID uint64, force bool) {
	if accountID == 0 || testing.Testing() {
		return
	}
	remoteDecorations.once.Do(func() {
		remoteDecorations.queue = make(chan remoteDecorationJob, 512)
		remoteDecorations.limiter = newHostLimiter(1)
		for range remoteDecorationWorkers {
			go func() {
				for job := range remoteDecorations.queue {
					runRemoteDecorationJob(job)
				}
			}()
		}
	})
	select {
	case remoteDecorations.queue <- remoteDecorationJob{accountID: accountID, force: force}:
	default:
	}
}

func runRemoteDecorationJob(job remoteDecorationJob) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic fetching avatar decorations for account %d: %v", job.accountID, r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var account models.Account
	if err := db.GetDB().WithContext(ctx).Where("id = ?", job.accountID).First(&account).Error; err != nil {
		return
	}
	release, err := remoteDecorations.limiter.acquire(ctx, account.OriginId)
	if err != nil {
		return
	}
	defer release()
	changed, err := RefreshRemoteAvatarDecorations(ctx, db.GetDB(), &account, job.force)
	if err != nil {
		log.Debug("Avatar decorations for %s not refreshed: %v", account.OriginId, err)
	}
	if changed {
		invalidateProfile(ctx, &account)
	}
}

// RefreshRemoteAvatarDecorations reads account's decorations from its
// Misskey-family origin and stores them. It reports whether the stored list
// changed. Non-Misskey servers, suspended hosts and recently checked accounts
// are skipped. A failed fetch keeps the previous list and still records the
// attempt, which is the backoff.
func RefreshRemoteAvatarDecorations(ctx context.Context, conn *gorm.DB, account *models.Account, force bool) (bool, error) {
	if account == nil || account.Domain == nil || account.OriginId == "" || account.Username == "" {
		return false, nil
	}
	now := time.Now()
	if last := account.AvatarDecorationsFetchedAt; last != nil {
		age := now.Sub(*last)
		if age < remoteDecorationForceMinAge || (!force && age < remoteDecorationRefreshTTL) {
			return false, nil
		}
	}
	host := profileBackfillHost(account)
	if host == "" {
		return false, nil
	}
	if suspended, err := hostSuspended(host); err != nil || suspended {
		return false, err
	}
	if remoteSoftware(ctx, host) != softwareMisskey {
		// Only stamp the row when it carries stale data, so ordinary
		// Mastodon accounts are never written to.
		if len(account.AvatarDecorations) == 0 {
			return false, nil
		}
		return storeRemoteDecorations(ctx, conn, account, nil, now)
	}
	list, err := fetchMisskeyAvatarDecorations(ctx, host, account.Username)
	if err != nil {
		_ = conn.WithContext(ctx).Model(&models.Account{}).Where("id = ?", account.Id).
			UpdateColumn("avatar_decorations_fetched_at", now).Error
		account.AvatarDecorationsFetchedAt = &now
		return false, err
	}
	return storeRemoteDecorations(ctx, conn, account, list, now)
}

func storeRemoteDecorations(ctx context.Context, conn *gorm.DB, account *models.Account, list models.AvatarDecorationSelections, now time.Time) (bool, error) {
	before, _ := account.AvatarDecorations.Value()
	after, _ := list.Value()
	if err := conn.WithContext(ctx).Model(&models.Account{}).Where("id = ?", account.Id).
		UpdateColumns(map[string]any{"avatar_decorations": list, "avatar_decorations_fetched_at": now}).Error; err != nil {
		return false, err
	}
	account.AvatarDecorations = list
	account.AvatarDecorationsFetchedAt = &now
	return before != after, nil
}

type misskeyDecoration struct {
	ID      string   `json:"id"`
	URL     string   `json:"url"`
	Angle   *float64 `json:"angle"`
	FlipH   *bool    `json:"flipH"`
	OffsetX *float64 `json:"offsetX"`
	OffsetY *float64 `json:"offsetY"`
}

func fetchMisskeyAvatarDecorations(ctx context.Context, host, username string) (models.AvatarDecorationSelections, error) {
	ctx, cancel := context.WithTimeout(ctx, remoteDecorationTimeout)
	defer cancel()
	var user struct {
		Username          string              `json:"username"`
		Host              *string             `json:"host"`
		AvatarDecorations []misskeyDecoration `json:"avatarDecorations"`
	}
	if err := remoteGetJSON(ctx, http.MethodPost, remoteReactionBase(host)+"/api/users/show", map[string]any{"username": username, "host": nil}, &user); err != nil {
		return nil, err
	}
	// The origin must answer for its own local user.
	if !strings.EqualFold(user.Username, username) || (user.Host != nil && *user.Host != "") {
		return nil, nil
	}
	var out models.AvatarDecorationSelections
	for _, d := range user.AvatarDecorations {
		if len(out) == models.MaxAvatarDecorations {
			break
		}
		if !validRemoteDecorationURL(d.URL) || d.ID == "" || len(d.ID) > 64 {
			continue
		}
		sel := models.AvatarDecorationSelection{ID: d.ID, URL: d.URL}
		if d.Angle != nil {
			sel.Angle = *d.Angle
		}
		if d.FlipH != nil {
			sel.FlipH = *d.FlipH
		}
		if d.OffsetX != nil {
			sel.OffsetX = *d.OffsetX
		}
		if d.OffsetY != nil {
			sel.OffsetY = *d.OffsetY
		}
		out = append(out, sel.Normalized())
	}
	return out, nil
}

func validRemoteDecorationURL(raw string) bool {
	if raw == "" || len(raw) > remoteDecorationMaxURL {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.Fragment == ""
}
