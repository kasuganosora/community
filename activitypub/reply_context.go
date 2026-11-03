package activitypub

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/systemsettings"
	"gorm.io/gorm"
)

const (
	// maxReplyAncestorFetches bounds signed GETs while a thread is opened.
	maxReplyAncestorFetches = 8
	// replyAncestorFetchBudget is the whole walk, not each request.
	replyAncestorFetchBudget = 20 * time.Second
	// replyParentRetryAfter backs off a parent that failed for good (404,
	// 401 after the signed retry) so opening the thread does not hammer it.
	replyParentRetryAfter = 10 * time.Minute
)

// replyContextResolver imports missing parents one at a time. It uses the
// signed fetch client: authorized-fetch servers reject unsigned GETs.
var replyContextResolver = func() *Resolver {
	resolver := NewResolver(nil, InstanceActorURI())
	resolver.noQuoteFetch = true
	resolver.skipReplyFetch = true
	return resolver
}

var replyParentFailures = struct {
	sync.Mutex
	until map[uint64]time.Time
}{}

func replyParentCoolingDown(id uint64) bool {
	replyParentFailures.Lock()
	defer replyParentFailures.Unlock()
	until, ok := replyParentFailures.until[id]
	if !ok {
		return false
	}
	if time.Now().After(until) {
		delete(replyParentFailures.until, id)
		return false
	}
	return true
}

func coolDownReplyParent(id uint64) {
	replyParentFailures.Lock()
	defer replyParentFailures.Unlock()
	if replyParentFailures.until == nil {
		replyParentFailures.until = map[uint64]time.Time{}
	}
	replyParentFailures.until[id] = time.Now().Add(replyParentRetryAfter)
}

// LoadMissingReplyAncestors links status to its reply parents. A parent that
// is not stored is fetched from the URI kept at ingest, or, when that URI was
// never stored, from a signed read of the reply's own object. Remote failures
// are logged and do not fail the thread response. Database errors are returned.
func LoadMissingReplyAncestors(ctx context.Context, status *models.Status) error {
	if status == nil || status.Id == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, replyAncestorFetchBudget)
	defer cancel()

	conn := db.GetDB().WithContext(ctx)
	base := systemsettings.GetBaseURL()
	current := status
	seen := map[uint64]bool{}
	fetches := 0
	for len(seen) < 40 {
		if current == nil || current.Id == 0 || seen[current.Id] {
			return nil
		}
		seen[current.Id] = true
		if err := models.LinkStoredReplyParents(conn, []*models.Status{current}, base); err != nil {
			return err
		}
		if current.InReplyToId != nil && *current.InReplyToId != 0 {
			parent, err := loadReplyAncestor(conn, *current.InReplyToId)
			if err != nil {
				return err
			}
			if parent == nil {
				return nil
			}
			current = parent
			continue
		}
		if fetches >= maxReplyAncestorFetches || ctx.Err() != nil || replyParentCoolingDown(current.Id) {
			return nil
		}
		uri, err := replyParentURI(ctx, conn, current)
		if err != nil {
			log.Warn("Could not read reply parent of status %d: %v", current.Id, err)
			if ctx.Err() == nil && !isTransientFetchError(err) {
				coolDownReplyParent(current.Id)
			}
			return nil
		}
		if uri == "" {
			return nil
		}
		if err = models.LinkStoredReplyParents(conn, []*models.Status{current}, base); err != nil {
			return err
		}
		if current.InReplyToId != nil && *current.InReplyToId != 0 {
			parent, err := loadReplyAncestor(conn, *current.InReplyToId)
			if err != nil {
				return err
			}
			current = parent
			continue
		}
		fetches++
		parent, err := importReplyParent(ctx, uri)
		if err != nil || parent == nil {
			if err == nil {
				err = gorm.ErrRecordNotFound
			}
			log.Warn("Failed to fetch reply parent %s of status %d: %v", uri, current.Id, err)
			if ctx.Err() == nil && !isTransientFetchError(err) {
				coolDownReplyParent(current.Id)
			}
			return nil
		}
		if err = models.LinkReplyParent(conn, current, parent); err != nil {
			return err
		}
		if current.InReplyToId == nil || *current.InReplyToId == 0 {
			return nil
		}
		next, err := loadReplyAncestor(conn, parent.Id)
		if err != nil {
			return err
		}
		current = next
	}
	return nil
}

func loadReplyAncestor(conn *gorm.DB, id uint64) (*models.Status, error) {
	var parent models.Status
	err := conn.Preload("Content").First(&parent, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &parent, nil
}

// replyParentURI is the stored inReplyTo address, or the one on the reply's
// own object when ingest did not keep it. An empty result means there is no
// parent to fetch.
func replyParentURI(ctx context.Context, conn *gorm.DB, status *models.Status) (string, error) {
	if uri := models.ReplyToURI(status); uri != "" {
		return uri, nil
	}
	if status.OriginUri == nil || *status.OriginUri == "" || models.ReplyParentChecked(status) {
		return "", nil
	}
	obj, _, err := replyContextResolver().fetchCanonicalObject(ctx, *status.OriginUri)
	if err != nil {
		return "", err
	}
	uri := noteInReplyTo(obj["inReplyTo"])
	if uri == "" || (status.OriginUri != nil && uri == *status.OriginUri) {
		if obj["inReplyTo"] != nil && uri == "" {
			log.Warn("Remote status %s has an inReplyTo value that is not a URI (%T)", *status.OriginUri, obj["inReplyTo"])
		}
		models.MarkReplyParentChecked(status)
		if err = conn.Model(status).Select("Properties").Updates(status).Error; err != nil {
			return "", err
		}
		return "", nil
	}
	models.SetReplyToURI(status, uri)
	if err = conn.Model(status).Select("Properties").Updates(status).Error; err != nil {
		return "", err
	}
	return uri, nil
}

// importReplyParent stores the public object at uri when this server does not
// already have it. The fetch is signed. The parent's own parent is not
// fetched here; the caller walks the chain.
func importReplyParent(ctx context.Context, uri string) (*models.Status, error) {
	if err := validateFederationURI(uri); err != nil {
		return nil, err
	}
	base := systemsettings.GetBaseURL()
	conn := db.GetDB().WithContext(ctx)
	if existing, err := models.FindReplyParent(conn, base, uri); err == nil && existing != nil && existing.Id != 0 {
		return existing, nil
	}
	resolver := replyContextResolver()
	obj, canonical, err := resolver.fetchCanonicalObject(ctx, uri)
	if err != nil {
		return nil, err
	}
	if canonical != uri {
		if existing, err := models.FindReplyParent(conn, base, canonical); err == nil && existing != nil && existing.Id != 0 {
			return existing, nil
		}
	}
	status, err := resolver.createStatusFromObject(obj, canonical)
	if err != nil {
		return nil, err
	}
	return &status, nil
}
