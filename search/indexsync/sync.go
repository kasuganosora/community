// Package indexsync keeps the Meilisearch indexes aligned with the database.
// It is imported for its side effects: when the engine is reachable it backfills
// existing rows, and status or profile changes update the index afterwards.
// When Meilisearch is absent every call returns without touching the database
// request path.
package indexsync

import (
	"context"
	"errors"
	"html"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/event"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/search"
)

func init() {
	search.SetReadyHook(reindexAll)
	search.SetAccountIndexer(indexAccountByID)
	search.SetReindexProgress(func() any { return Progress() })
	event.Register("status", "*", &event.Handler{Exec: onStatus})
}

// Start begins probing Meilisearch. Call it after database migrations so the
// first backfill sees the real schema. Search requests also start the client,
// so a missed call still falls through to the database until the engine is up.
func Start() {
	search.GetMeilisearchClient()
}

// Shutdown flushes the batched incremental index writes (one final request
// per index) and stops the queue. Call it once request handlers have
// drained. Whatever cannot be sent is repaired by the backfill on the next
// start.
func Shutdown(ctx context.Context) error {
	return search.ShutdownIndexQueue(ctx)
}

func onStatus(ev event.Event) bool {
	status, ok := ev.Data.(*models.Status)
	if !ok || status == nil || status.Id == 0 {
		return true
	}
	switch ev.Name {
	case "after_create", "after_update", "after_save":
		go indexStatusByID(status.Id)
	case "after_delete":
		// Queued, never waits for Meilisearch (see search.IndexQueue).
		if err := search.GetMeilisearchClient().QueueDelete(search.IndexStatuses, strconv.FormatUint(status.Id, 10)); err != nil {
			log.Warn("search index delete status %d: %v", status.Id, err)
		}
	}
	return true
}

var (
	reindexMu       sync.Mutex
	reindexAttempts int
)

func reindexAll() {
	reindexMu.Lock()
	defer reindexMu.Unlock()
	reindexAttempts = 0
	reindexLocked()
}

func reindexLocked() {
	client := search.GetMeilisearchClient()
	if !client.IsEnabled() || db.GetDB() == nil {
		scheduleReindex()
		return
	}
	// Resume from the last batch Meili acknowledged (survives Meili crashes
	// and our own restarts); a finished pass clears the checkpoint.
	cp := loadCheckpoint()
	for _, step := range []struct {
		name string
		run  func(*search.MeilisearchClient, map[string]uint64) error
	}{{"accounts", reindexAccounts}, {"statuses", reindexStatuses}, {"tags", reindexTags}} {
		if err := step.run(client, cp); err != nil {
			stopProgress(step.name)
			log.Warn("search index %s: %v (will resume from checkpoint %d)", step.name, err, cp[step.name])
			scheduleReindex()
			return
		}
	}
	saveCheckpoint(map[string]uint64{})
	log.Info("Meilisearch index backfill finished")
}

// countRows reports (done, total) for progress, honouring the checkpoint.
func countRows(table, where string, last uint64) (int64, int64) {
	var total, done int64
	_ = db.GetDB().Table(table).Where(where).Count(&total).Error
	if last > 0 {
		_ = db.GetDB().Table(table).Where(where).Where("id <= ?", last).Count(&done).Error
	}
	return done, total
}

// commitBatch writes one batch under the write-slot limit and checkpoints it.
func commitBatch(index string, cp map[string]uint64, last uint64, n int, write func() error) error {
	if err := withWriteSlot(write); err != nil {
		return err
	}
	cp[index] = last
	saveCheckpoint(cp)
	advanceProgress(index, int64(n), false)
	if reindexPause > 0 {
		time.Sleep(reindexPause)
	}
	return nil
}

func scheduleReindex() {
	if reindexAttempts >= 8 {
		log.Warn("Meilisearch backfill paused until the engine reconnects")
		return
	}
	reindexAttempts++
	go func() {
		time.Sleep(2 * time.Second)
		reindexMu.Lock()
		defer reindexMu.Unlock()
		reindexLocked()
	}()
}

func reindexAccounts(client *search.MeilisearchClient, cp map[string]uint64) error {
	last := cp["accounts"]
	done, total := countRows("accounts", "deleted_at IS NULL", last)
	beginProgress("accounts", done, total)
	for client.IsEnabled() {
		var accounts []*models.Account
		err := db.GetDB().Where("id > ? AND deleted_at IS NULL", last).Order("id").Limit(reindexBatch).Find(&accounts).Error
		if err != nil {
			return err
		}
		if len(accounts) == 0 {
			advanceProgress("accounts", 0, true)
			return nil
		}
		docs := make([]*search.AccountSearchDocument, 0, len(accounts))
		for _, account := range accounts {
			if doc := accountDoc(account); doc != nil {
				docs = append(docs, doc)
			}
			last = account.Id
		}
		if err = commitBatch("accounts", cp, last, len(accounts), func() error { return client.BatchIndexAccounts(docs) }); err != nil {
			return err
		}
	}
	return errEngineGone
}

func reindexStatuses(client *search.MeilisearchClient, cp map[string]uint64) error {
	last := cp["statuses"]
	done, total := countRows("statuses", "deleted_at IS NULL AND scheduled_at IS NULL", last)
	beginProgress("statuses", done, total)
	for client.IsEnabled() {
		var statuses []*models.Status
		err := db.GetDB().Where("id > ? AND deleted_at IS NULL AND scheduled_at IS NULL", last).Order("id").Limit(reindexBatch).Find(&statuses).Error
		if err != nil {
			return err
		}
		if len(statuses) == 0 {
			advanceProgress("statuses", 0, true)
			return nil
		}
		ids := make([]uint64, len(statuses))
		for i, status := range statuses {
			ids[i] = status.Id
			last = status.Id
		}
		contents := loadContents(ids)
		authors := loadAuthors(statuses)
		docs := make([]*search.StatusSearchDocument, 0, len(statuses))
		for _, status := range statuses {
			if doc := statusDoc(status, contents[status.Id], authors[status.AccountId]); doc != nil {
				docs = append(docs, doc)
			}
		}
		if err = commitBatch("statuses", cp, last, len(statuses), func() error { return client.BatchIndexStatuses(docs) }); err != nil {
			return err
		}
	}
	return errEngineGone
}

func reindexTags(client *search.MeilisearchClient, cp map[string]uint64) error {
	last := cp["tags"]
	done, total := countRows("tags", "1 = 1", last)
	beginProgress("tags", done, total)
	for client.IsEnabled() {
		var tags []*models.Tag
		err := db.GetDB().Where("id > ?", last).Order("id").Limit(reindexBatch).Find(&tags).Error
		if err != nil {
			return err
		}
		if len(tags) == 0 {
			advanceProgress("tags", 0, true)
			return nil
		}
		docs := make([]*search.HashtagSearchDocument, 0, len(tags))
		for _, tag := range tags {
			last = tag.Id
			if doc := tagDoc(tag); doc != nil {
				docs = append(docs, doc)
			}
		}
		if err = commitBatch("tags", cp, last, len(tags), func() error { return client.BatchIndexHashtags(docs) }); err != nil {
			return err
		}
	}
	return errEngineGone
}

// errEngineGone stops a pass when Meili disconnects mid-way; the checkpoint
// keeps what was acknowledged and the ready hook resumes it on reconnect.
var errEngineGone = errors.New("meilisearch became unavailable")

func indexStatusByID(id uint64) {
	client := search.GetMeilisearchClient()
	if !client.IsEnabled() || db.GetDB() == nil {
		return
	}
	defer func() {
		// The database can be closed (shutdown, tests) while this event
		// goroutine runs; indexing is best effort and must not crash.
		if r := recover(); r != nil {
			log.Warn("search index status %d skipped: %v", id, r)
		}
	}()
	var status models.Status
	if err := db.GetDB().First(&status, id).Error; err != nil {
		return
	}
	if status.DeletedAt != nil || status.IsScheduled() {
		_ = client.QueueDelete(search.IndexStatuses, strconv.FormatUint(id, 10))
		return
	}
	contents := loadContents([]uint64{id})
	// The content row can land just after the status event. One short wait
	// covers that without blocking the request that fired the event.
	if contents[id].Content == "" && contents[id].SpoilerText == "" {
		time.Sleep(200 * time.Millisecond)
		contents = loadContents([]uint64{id})
	}
	var author models.Account
	_ = db.GetDB().First(&author, status.AccountId).Error
	doc := statusDoc(&status, contents[id], &author)
	if doc == nil {
		return
	}
	// Incremental updates go through the batching queue: repeated updates
	// of one status, its author and its tags within a flush interval become
	// one document each in one request per index.
	if err := client.QueueStatus(doc); err != nil {
		log.Warn("search index status %d: %v", id, err)
		return
	}
	if err := client.QueueAccount(accountDoc(&author)); err != nil {
		log.Warn("search index account %d: %v", author.Id, err)
	}
	indexStatusTags(client, id)
}

func indexAccountByID(id uint64) {
	client := search.GetMeilisearchClient()
	if !client.IsEnabled() || db.GetDB() == nil {
		return
	}
	var account models.Account
	if err := db.GetDB().First(&account, id).Error; err != nil {
		return
	}
	if err := client.QueueAccount(accountDoc(&account)); err != nil {
		log.Warn("search index account %d: %v", id, err)
	}
}

func indexStatusTags(client *search.MeilisearchClient, statusID uint64) {
	var tags []*models.Tag
	err := db.GetDB().Table("tags").
		Joins("JOIN status_tags ON status_tags.tag_id = tags.id").
		Where("status_tags.status_id = ?", statusID).
		Find(&tags).Error
	if err != nil {
		return
	}
	for _, tag := range tags {
		doc := tagDoc(tag)
		if doc == nil {
			continue
		}
		if err = client.QueueHashtag(doc); err != nil {
			log.Warn("search index tag %s: %v", tag.Name, err)
		}
	}
}

func loadContents(ids []uint64) map[uint64]models.StatusContent {
	out := map[uint64]models.StatusContent{}
	if len(ids) == 0 {
		return out
	}
	var rows []models.StatusContent
	conn := db.GetDB()
	if conn == nil {
		return out
	}
	if err := conn.Where("status_id IN ?", ids).Find(&rows).Error; err != nil {
		return out
	}
	for _, row := range rows {
		out[row.StatusId] = row
	}
	return out
}

func loadAuthors(statuses []*models.Status) map[uint64]*models.Account {
	out := map[uint64]*models.Account{}
	ids := make([]uint64, 0, len(statuses))
	seen := map[uint64]bool{}
	for _, status := range statuses {
		if status.AccountId > 0 && !seen[status.AccountId] {
			seen[status.AccountId] = true
			ids = append(ids, status.AccountId)
		}
	}
	if len(ids) == 0 {
		return out
	}
	var accounts []*models.Account
	if err := db.GetDB().Where("id IN ?", ids).Find(&accounts).Error; err != nil {
		return out
	}
	for _, account := range accounts {
		out[account.Id] = account
	}
	return out
}

func accountDoc(account *models.Account) *search.AccountSearchDocument {
	if account == nil || account.Id == 0 {
		return nil
	}
	domain := ""
	if account.Domain != nil {
		domain = *account.Domain
	}
	created := int64(0)
	if account.CreatedAt != nil {
		created = account.CreatedAt.Unix()
	}
	return &search.AccountSearchDocument{
		ID:          strconv.FormatUint(account.Id, 10),
		Username:    account.Username,
		DisplayName: plainText(account.DisplayName),
		Domain:      domain,
		Bio:         plainText(account.Note),
		CreatedAt:   created,
	}
}

func statusDoc(status *models.Status, content models.StatusContent, author *models.Account) *search.StatusSearchDocument {
	if status == nil || status.Id == 0 {
		return nil
	}
	username := ""
	if author != nil {
		username = author.Username
	}
	created := int64(0)
	if status.CreatedAt != nil {
		created = status.CreatedAt.Unix()
	}
	return &search.StatusSearchDocument{
		ID:          strconv.FormatUint(status.Id, 10),
		Content:     plainText(content.Content),
		SpoilerText: plainText(content.SpoilerText),
		AccountID:   status.AccountId,
		Username:    username,
		Visibility:  int(status.Visibility),
		CreatedAt:   created,
	}
}

func tagDoc(tag *models.Tag) *search.HashtagSearchDocument {
	if tag == nil {
		return nil
	}
	name := models.NormalizeTagName(tag.Name)
	if !models.ValidTagName(name) {
		return nil // never index garbage names (they are not valid ids either)
	}
	created := int64(0)
	if tag.CreatedAt != nil {
		created = tag.CreatedAt.Unix()
	}
	return &search.HashtagSearchDocument{
		// Keyed by the case-insensitive tag key so case variants map to
		// one document; Name keeps the display spelling.
		ID:          search.HashtagDocumentID(models.TagKey(name)),
		Name:        name,
		StatusCount: int64(tag.StatusCount),
		CreatedAt:   created,
	}
}

func plainText(value string) string {
	var b strings.Builder
	inTag := false
	for _, r := range value {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(html.UnescapeString(b.String()))
}
