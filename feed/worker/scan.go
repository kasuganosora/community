package worker

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

type targetGroup struct {
	kind       string
	recipients []models.FeedRecipient
}

func copyCursor(source map[string]uint64) map[string]uint64 {
	result := make(map[string]uint64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

// fixedRecipients is derived only from immutable event metadata. Filtering
// against current accounts before offset pagination would shift later pages
// when an earlier account is deleted. Delivery resolves each bounded batch
// against current SQL and excludes remote/deleted accounts there.
func fixedRecipients(ids []uint64) []models.FeedRecipient {
	unique := make(map[uint64]bool, len(ids))
	ordered := make([]uint64, 0, len(ids))
	for _, id := range ids {
		if id != 0 && !unique[id] {
			unique[id] = true
			ordered = append(ordered, id)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	result := make([]models.FeedRecipient, 0, len(ordered))
	for _, id := range ordered {
		result = append(result, models.FeedRecipient{AccountID: id})
	}
	return result
}

// Variants are bounded and explicit: a global public index never accidentally
// includes private content, and filtered caches receive the same invalidation.
func publicVariants() []models.FeedRecipient {
	result := make([]models.FeedRecipient, 0, 6)
	for _, variant := range []string{"all", "local", "remote", "media", "media:local", "media:remote"} {
		result = append(result, models.FeedRecipient{TimelineID: variant})
	}
	return result
}

func tagVariants(names []string) []models.FeedRecipient {
	seen := map[string]bool{}
	result := []models.FeedRecipient{}
	for _, name := range names {
		name = models.TagKey(name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		// TimelineID is JSON, rather than a delimiter in the user supplied tag.
		for _, variant := range []string{"", "local:", "remote:", "media:", "local:media:", "remote:media:"} {
			result = append(result, models.FeedRecipient{TimelineID: variant + name})
		}
	}
	return result
}

func (w *Worker) fixedTargets(_ context.Context, event *models.FeedEvent) ([]targetGroup, error) {
	m := event.Metadata
	m.RecipientIDs = append(append([]uint64{}, m.RecipientIDs...), m.PreviousRecipientIDs...)
	m.TagNames = append(append([]string{}, m.TagNames...), m.PreviousTagNames...)
	if event.Kind == models.FeedNotification {
		return []targetGroup{{"notification", fixedRecipients(m.RecipientIDs)}}, nil
	}
	if event.Kind == models.FeedInvalidate {
		ids := append(append([]uint64{}, m.RecipientIDs...), m.ActorID)
		locals := fixedRecipients(ids)
		lists := []models.FeedRecipient{}
		for _, id := range m.ListIDs {
			lists = append(lists, models.FeedRecipient{ListID: id})
		}
		groups := []targetGroup{{"account_invalidate", locals}, {"list_invalidate", lists}}
		if globalInvalidation(event) {
			groups = append(groups, targetGroup{"public_invalidate", publicVariants()})
		}
		return groups, nil
	}
	ids := append(append([]uint64{}, m.RecipientIDs...), event.ActorID)
	locals := fixedRecipients(ids)
	mentions := fixedRecipients(m.RecipientIDs)
	groups := []targetGroup{{"home", locals}, {"mention", mentions}}
	if m.Visibility == models.StatusVisibilityDirect {
		groups = append(groups, targetGroup{"direct", locals})
	}
	if m.Visibility != models.StatusVisibilityDirect || event.Kind == models.FeedStatusRefresh || event.Kind == models.FeedStatusDelete {
		// Refresh reconciles both old and new audience indexes. The actual
		// projection checks current SQL visibility before adding anything.
		if m.Visibility == models.StatusVisibilityPublic || event.Kind == models.FeedStatusRefresh || event.Kind == models.FeedStatusDelete {
			groups = append(groups, targetGroup{"public", publicVariants()}, targetGroup{"tag", tagVariants(m.TagNames)})
		}
	}
	return groups, nil
}

func globalInvalidation(event *models.FeedEvent) bool {
	return event.Kind == models.FeedInvalidate && (event.Metadata.Reason == "domain_policy_changed" || event.Metadata.Reason == "account_policy_changed" || (event.Metadata.ActorID == 0 && len(event.Metadata.ListIDs) == 0))
}

// scan produces at most four durable batches per claim. Cursor movement and
// batch insertion commit together; a low-ID transaction committing late is
// covered by its own relationship event rather than an event-ID watermark.
func (w *Worker) scan(ctx context.Context, event *models.FeedEvent) error {
	cursor, upper := copyCursor(event.ScanCursor), copyCursor(event.ScanUpperBound)
	groups, err := w.fixedTargets(ctx, event)
	if err != nil {
		return err
	}
	batches := make([]models.FeedBatch, 0, 4)
	appendBatch := func(kind string, recipients []models.FeedRecipient) {
		if len(recipients) == 0 {
			return
		}
		seq := "batch:" + kind
		batches = append(batches, models.FeedBatch{EventID: event.ID, TargetKind: kind, BatchNo: cursor[seq], Recipients: recipients})
		cursor[seq]++
	}
	complete := true
	for _, group := range groups {
		// Legacy offsets counted only then-local accounts, in the same ID
		// order. The immutable sequence is a superset: reusing that offset
		// can replay already registered recipients but cannot skip a pending
		// one. Keep batch counters so replay never overwrites old batches.
		key := "fixed:" + group.kind
		for int(cursor[key]) < len(group.recipients) && len(batches) < 4 {
			end := min(int(cursor[key])+w.Options.BatchSize, len(group.recipients))
			appendBatch(group.kind, group.recipients[int(cursor[key]):end])
			cursor[key] = uint64(end)
		}
		if int(cursor[key]) < len(group.recipients) {
			complete = false
		}
	}
	conn := w.DB.WithContext(ctx)
	if event.Kind != models.FeedInvalidate && event.Kind != models.FeedNotification && (event.Metadata.Visibility != models.StatusVisibilityDirect || event.Kind == models.FeedStatusRefresh || event.Kind == models.FeedStatusDelete) {
		var done bool
		done, err = w.scanFollowers(conn, event, cursor, upper, &batches)
		if err != nil {
			return err
		}
		complete = complete && done
		done, err = w.scanLists(conn, event, cursor, upper, &batches, false)
		if err != nil {
			return err
		}
		complete = complete && done
	}
	if globalInvalidation(event) {
		var done bool
		done, err = w.scanLocalAccounts(conn, event, cursor, upper, &batches)
		if err != nil {
			return err
		}
		complete = complete && done
		done, err = w.scanLists(conn, event, cursor, upper, &batches, true)
		if err != nil {
			return err
		}
		complete = complete && done
		done, err = w.scanTags(conn, event, cursor, upper, &batches)
		if err != nil {
			return err
		}
		complete = complete && done
	}
	return models.PersistFeedBatches(conn, event.LeaseRef(), batches, cursor, upper, complete)
}

func setUpper(conn *gorm.DB, model any, column, kind string, upper map[string]uint64) error {
	if _, exists := upper[kind]; exists {
		return nil
	}
	var id uint64
	if err := conn.Model(model).Select("COALESCE(MAX(" + column + "), 0)").Scan(&id).Error; err != nil {
		return err
	}
	upper[kind] = id
	return nil
}

func (w *Worker) scanFollowers(conn *gorm.DB, event *models.FeedEvent, cursor, upper map[string]uint64, batches *[]models.FeedBatch) (bool, error) {
	const kind = "followers"
	if cursor[kind+":done"] == 1 {
		return true, nil
	}
	query := conn.Model(&models.Follow{}).Where("follow_id = ?", event.ActorID)
	if err := setUpper(query, &models.Follow{}, "id", kind, upper); err != nil {
		return false, err
	}
	for len(*batches) < 4 {
		var follows []models.Follow
		threshold := time.Now().Add(-w.Options.ActiveWindow)
		err := conn.Model(&models.Follow{}).Select("follows.id, follows.account_id").Joins("JOIN accounts ON accounts.id = follows.account_id").
			Where("follows.follow_id = ? AND follows.id > ? AND follows.id <= ? AND follows.status = ?", event.ActorID, cursor[kind], upper[kind], models.FollowStatusFollowed).
			Where("accounts.domain IS NULL AND accounts.deleted_at IS NULL").
			Where("accounts.feed_last_seen_at > ? OR accounts.last_activity_at > ? OR accounts.created_at > ?", threshold, threshold, threshold).
			Order("follows.id").Limit(w.Options.BatchSize).Find(&follows).Error
		if err != nil {
			return false, err
		}
		if len(follows) == 0 {
			cursor[kind+":done"] = 1
			return true, nil
		}
		recipients := make([]models.FeedRecipient, 0, len(follows))
		seen := map[uint64]bool{}
		for _, follow := range follows {
			if !seen[follow.AccountId] {
				recipients = append(recipients, models.FeedRecipient{AccountID: follow.AccountId})
				seen[follow.AccountId] = true
			}
			cursor[kind] = follow.Id
		}
		seq := "batch:home"
		*batches = append(*batches, models.FeedBatch{EventID: event.ID, TargetKind: "home", BatchNo: cursor[seq], Recipients: recipients})
		cursor[seq]++
	}
	return false, nil
}

func (w *Worker) scanLists(conn *gorm.DB, event *models.FeedEvent, cursor, upper map[string]uint64, batches *[]models.FeedBatch, all bool) (bool, error) {
	const kind = "lists"
	if cursor[kind+":done"] == 1 {
		return true, nil
	}
	if err := setUpper(conn, &models.List{}, "id", kind, upper); err != nil {
		return false, err
	}
	for len(*batches) < 4 {
		query := conn.Model(&models.List{}).Where("id > ? AND id <= ?", cursor[kind], upper[kind])
		if !all {
			fresh := conn.Session(&gorm.Session{NewDB: true})
			members := fresh.Model(&models.ListAccount{}).Where("account_id = ?", event.ActorID).Select("list_id")
			if tags := event.Metadata.TagIDs; len(tags) > 0 && event.Metadata.Visibility != models.StatusVisibilityDirect && event.Metadata.Visibility != models.StatusVisibilityPrivate {
				// Lists following one of the post's hashtags (tag_id is
				// indexed), unless the owner and author block each other.
				byTag := fresh.Model(&models.ListHashtag{}).Where("tag_id IN ?", tags).Select("list_id")
				blocked := fresh.Model(&models.Block{}).Select("1").Where("(blocks.type = ? AND ((blocks.account_id = lists.account_id AND blocks.target_account_id = ?) OR (blocks.account_id = ? AND blocks.target_account_id = lists.account_id)) OR (blocks.type = ? AND blocks.account_id = lists.account_id AND blocks.target_domain = (SELECT accounts.domain FROM accounts WHERE accounts.id = ?)))", models.BlockTypeAccount, event.ActorID, event.ActorID, models.BlockTypeDomain, event.ActorID)
				if event.Metadata.ReplyID != 0 {
					byTag = byTag.Where("list_id NOT IN (?)", fresh.Model(&models.List{}).Where("replies_policy = ?", models.ListRepliesPolicyNone).Select("id"))
				}
				query = query.Where("id IN (?) OR (id IN (?) AND NOT EXISTS (?))", members, byTag, blocked)
			} else {
				query = query.Where("id IN (?)", members)
			}
		}
		var rows []models.List
		if err := query.Select("id, account_id").Order("id").Limit(w.Options.BatchSize).Find(&rows).Error; err != nil {
			return false, err
		}
		if len(rows) == 0 {
			cursor[kind+":done"] = 1
			return true, nil
		}
		recipients := make([]models.FeedRecipient, 0, len(rows))
		for _, row := range rows {
			recipients = append(recipients, models.FeedRecipient{ListID: row.Id, AccountID: row.AccountId})
			cursor[kind] = row.Id
		}
		target := "list"
		if all {
			target = "list_invalidate"
		}
		seq := "batch:" + target
		*batches = append(*batches, models.FeedBatch{EventID: event.ID, TargetKind: target, BatchNo: cursor[seq], Recipients: recipients})
		cursor[seq]++
	}
	return false, nil
}

func (w *Worker) scanLocalAccounts(conn *gorm.DB, event *models.FeedEvent, cursor, upper map[string]uint64, batches *[]models.FeedBatch) (bool, error) {
	const kind = "accounts"
	if cursor[kind+":done"] == 1 {
		return true, nil
	}
	if err := setUpper(conn, &models.Account{}, "id", kind, upper); err != nil {
		return false, err
	}
	for len(*batches) < 4 {
		var ids []uint64
		if err := conn.Model(&models.Account{}).Where("id > ? AND id <= ? AND domain IS NULL", cursor[kind], upper[kind]).Order("id").Limit(w.Options.BatchSize).Pluck("id", &ids).Error; err != nil {
			return false, err
		}
		if len(ids) == 0 {
			cursor[kind+":done"] = 1
			return true, nil
		}
		recipients := []models.FeedRecipient{}
		for _, id := range ids {
			recipients = append(recipients, models.FeedRecipient{AccountID: id})
			cursor[kind] = id
		}
		seq := "batch:account_invalidate"
		*batches = append(*batches, models.FeedBatch{EventID: event.ID, TargetKind: "account_invalidate", BatchNo: cursor[seq], Recipients: recipients})
		cursor[seq]++
	}
	return false, nil
}

func (w *Worker) scanTags(conn *gorm.DB, event *models.FeedEvent, cursor, upper map[string]uint64, batches *[]models.FeedBatch) (bool, error) {
	const kind = "tags"
	if cursor[kind+":done"] == 1 {
		return true, nil
	}
	if err := setUpper(conn, &models.Tag{}, "id", kind, upper); err != nil {
		return false, err
	}
	for len(*batches) < 4 {
		var tags []models.Tag
		if err := conn.Where("id > ? AND id <= ?", cursor[kind], upper[kind]).Order("id").Limit(max(1, w.Options.BatchSize/6)).Find(&tags).Error; err != nil {
			return false, err
		}
		if len(tags) == 0 {
			cursor[kind+":done"] = 1
			return true, nil
		}
		names := []string{}
		for _, tag := range tags {
			names = append(names, tag.Name)
			cursor[kind] = tag.Id
		}
		seq := "batch:tag_invalidate"
		*batches = append(*batches, models.FeedBatch{EventID: event.ID, TargetKind: "tag_invalidate", BatchNo: cursor[seq], Recipients: tagVariants(names)})
		cursor[seq]++
	}
	return false, nil
}

// Stable recipient signatures aid fixture/oracle tooling without logging IDs.
func RecipientSignature(batch models.FeedBatch) string {
	keys := make([]string, 0, len(batch.Recipients))
	for _, r := range batch.Recipients {
		keys = append(keys, strconv.FormatUint(r.AccountID, 10)+"/"+strconv.FormatUint(r.ListID, 10)+"/"+r.TimelineID)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
