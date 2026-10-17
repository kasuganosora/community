package activitypub

import (
	"reflect"

	"context"
	"github.com/kasuganosora/community/db/dialect"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

const (
	maxRemoteProfileFields = 16
	maxRemoteFieldName     = 255
	maxRemoteFieldValue    = 2047
)

// remoteProfileFields maps an actor's PropertyValue attachments (Mastodon,
// Misskey and Pleroma profile metadata) to account fields. Names are plain
// text; values keep only sanitized HTML.
func remoteProfileFields(actor *Actor) []models.AccountField {
	fields := make([]models.AccountField, 0)
	if actor == nil {
		return fields
	}
	for _, a := range actor.Attachments {
		if a.Type != "PropertyValue" {
			continue
		}
		name := truncateRunes(strings.TrimSpace(htmlsafe.StripTags(a.Name)), maxRemoteFieldName)
		value := truncateRunes(htmlsafe.Sanitize(a.Value), maxRemoteFieldValue)
		if name == "" && value == "" {
			continue
		}
		fields = append(fields, models.AccountField{Name: name, Val: value})
		if len(fields) == maxRemoteProfileFields {
			break
		}
	}
	return fields
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// applyRemoteProfileFields stores the actor's profile fields on the account.
func applyRemoteProfileFields(tx *gorm.DB, accountID uint64, actor *Actor) error {
	if accountID == 0 {
		return nil
	}
	fields := remoteProfileFields(actor)
	var current models.AccountExtra
	// Skip the write when the stored fields are already identical.
	if err := tx.Select("fields").Where("account_id = ?", accountID).Take(&current).Error; err == nil &&
		reflect.DeepEqual(normalizeFields(current.Fields), normalizeFields(fields)) {
		return nil
	}
	return tx.Model(&models.AccountExtra{}).Where("account_id = ?", accountID).
		Select("fields").Updates(&models.AccountExtra{Fields: fields}).Error
}

func normalizeFields(f []models.AccountField) []models.AccountField {
	if len(f) == 0 {
		return nil
	}
	return f
}

// Remote follower / following / post totals come from the actor's
// collections (totalItems), as on Mastodon. Fetching them is best effort,
// bounded and off the request path: a relay can introduce hundreds of new
// actors a minute, so excess jobs are dropped and picked up on the next
// profile refresh.
type collectionCountJob struct {
	accountID uint64
	actor     Actor
}

var collectionCounts struct {
	once  sync.Once
	queue chan collectionCountJob
}

const collectionCountWorkers = 2

func scheduleRemoteCollectionCounts(accountID uint64, actor *Actor) {
	if accountID == 0 || actor == nil || testing.Testing() {
		return
	}
	if actor.Followers == "" && actor.Following == "" && actor.Outbox == "" {
		return
	}
	collectionCounts.once.Do(func() {
		collectionCounts.queue = make(chan collectionCountJob, 512)
		for range collectionCountWorkers {
			go func() {
				for job := range collectionCounts.queue {
					runCollectionCountJob(job)
				}
			}()
		}
	})
	select {
	case collectionCounts.queue <- collectionCountJob{accountID: accountID, actor: *actor}:
	default:
	}
}

func runCollectionCountJob(job collectionCountJob) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic fetching collection totals for %s: %v", job.actor.ID, r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := syncRemoteCollectionCounts(ctx, db.GetDB(), NewFetchClient(), job.accountID, &job.actor); err != nil {
		log.Warn("Failed to store collection totals for %s: %v", job.actor.ID, err)
	}
}

type objectFetcher interface {
	FetchObject(ctx context.Context, objectURI string) (map[string]any, error)
}

// syncRemoteCollectionCounts reads totalItems from the actor's followers,
// following and outbox collections. A hidden or unreachable collection
// leaves the stored counter unchanged.
func syncRemoteCollectionCounts(ctx context.Context, conn *gorm.DB, client objectFetcher, accountID uint64, actor *Actor) error {
	host := ""
	if u, err := url.Parse(actor.ID); err == nil {
		host = strings.ToLower(u.Host)
	}
	updates := map[string]any{}
	for column, uri := range map[string]string{
		"followers_count": actor.Followers,
		"following_count": actor.Following,
		"statuses_count":  actor.Outbox,
	} {
		if total, ok := collectionTotal(ctx, client, uri, host); ok {
			updates[column] = total
		}
	}
	if len(updates) == 0 {
		return nil
	}
	return dialect.UpdateChanged(conn.Model(&models.AccountExtra{}).Where("account_id = ?", accountID), updates, nil).Error
}

func collectionTotal(ctx context.Context, client objectFetcher, uri, actorHost string) (int, bool) {
	if uri == "" || actorHost == "" {
		return 0, false
	}
	u, err := url.Parse(uri)
	// Only the actor's own server can vouch for its collections.
	if err != nil || strings.ToLower(u.Host) != actorHost {
		return 0, false
	}
	obj, err := client.FetchObject(ctx, uri)
	if err != nil {
		return 0, false
	}
	total, ok := obj["totalItems"].(float64)
	if !ok || total < 0 || total > 1<<31-1 {
		return 0, false
	}
	return int(total), true
}
