package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/direct"
	"github.com/kasuganosora/community/feed/home"
	"github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/feed/mention"
	"github.com/kasuganosora/community/feed/notification"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/feed/tag"
	"github.com/kasuganosora/community/models"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type deliveryTarget struct {
	timeline feed.Timeline
	account  *models.Account
	kind     string
	list     *models.List
}

func (w *Worker) targets(ctx context.Context, batch *models.FeedBatch) ([]deliveryTarget, error) {
	accountIDs, listIDs := []uint64{}, []uint64{}
	for _, recipient := range batch.Recipients {
		if recipient.AccountID != 0 {
			accountIDs = append(accountIDs, recipient.AccountID)
		}
		if recipient.ListID != 0 {
			listIDs = append(listIDs, recipient.ListID)
		}
	}
	conn := w.DB.WithContext(ctx)
	accounts := map[uint64]*models.Account{}
	if len(accountIDs) > 0 {
		var rows []*models.Account
		if err := conn.Where("id IN ? AND domain IS NULL", accountIDs).Preload("User").Preload("User.Settings").Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, account := range rows {
			accounts[account.Id] = account
		}
	}
	lists := map[uint64]*models.List{}
	if len(listIDs) > 0 {
		var rows []*models.List
		if err := conn.Where("id IN ?", listIDs).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			lists[row.Id] = row
		}
	}
	kind := strings.TrimSuffix(batch.TargetKind, "_invalidate")
	result := []deliveryTarget{}
	seen := map[string]bool{}
	add := func(t feed.Timeline, account *models.Account, l *models.List) {
		key := feed.TimelineKey(t)
		if !seen[key] {
			result = append(result, deliveryTarget{t, account, kind, l})
			seen[key] = true
		}
	}
	for _, recipient := range batch.Recipients {
		account := accounts[recipient.AccountID]
		switch kind {
		case "account":
			if account != nil {
				add(&home.HomeTimeline{Account: account}, account, nil)
				add(&mention.MentionTimeline{Account: account}, account, nil)
				add(&direct.DirectTimeline{Account: account}, account, nil)
				add(&notification.NotificationTimeline{Account: account}, account, nil)
			}
		case "home":
			if account != nil {
				add(&home.HomeTimeline{Account: account}, account, nil)
			}
		case "mention":
			if account != nil {
				add(&mention.MentionTimeline{Account: account}, account, nil)
			}
		case "direct":
			if account != nil {
				add(&direct.DirectTimeline{Account: account}, account, nil)
			}
		case "notification":
			if account != nil {
				add(&notification.NotificationTimeline{Account: account}, account, nil)
			}
		case "public":
			id := recipient.TimelineID
			add(&public.PublicTimeline{OnlyLocal: strings.Contains(id, "local"), OnlyRemote: strings.Contains(id, "remote"), OnlyMedia: strings.Contains(id, "media")}, nil, nil)
		case "tag":
			parts := strings.Split(recipient.TimelineID, ":")
			flags := strings.Join(parts[:len(parts)-1], ":")
			add(&tag.TagTimeline{Tag: parts[len(parts)-1], OnlyLocal: strings.Contains(flags, "local"), OnlyRemote: strings.Contains(flags, "remote"), OnlyMedia: strings.Contains(flags, "media")}, nil, nil)
		case "list":
			l := lists[recipient.ListID]
			if l == nil {
				continue
			}
			for _, flags := range []string{"", "local", "remote", "media", "media:local", "media:remote"} {
				add(&list.ListTimeline{List: l, OnlyLocal: strings.Contains(flags, "local"), OnlyRemote: strings.Contains(flags, "remote"), OnlyMedia: strings.Contains(flags, "media")}, account, l)
			}
		default:
			return nil, fmt.Errorf("%w: unsupported target kind", ErrInvalidTask)
		}
	}
	return result, nil
}

func (w *Worker) deliver(ctx context.Context, event *models.FeedEvent, batch *models.FeedBatch) error {
	targets, err := w.targets(ctx, batch)
	if err != nil || len(targets) == 0 {
		return err
	}
	// Cold indexes are bootstrapped as incomplete, not as empty ready feeds.
	// This avoids a SQL timeline rebuild for every follower of a large author.
	// SQL is deliberately reloaded AFTER capturing these epochs. A restarted
	// attempt cannot transplant a stale snapshot into a newly initialized cache.
	timelines := make([]feed.Timeline, len(targets))
	for i := range targets {
		timelines[i] = targets[i].timeline
	}
	epochs, epochErrors := feed.PrepareProjectionEpochs(ctx, timelines)
	for _, err := range epochErrors {
		if err != nil {
			return err
		}
	}
	if event.Kind == models.FeedInvalidate {
		now, err := models.FeedDatabaseTime(w.DB.WithContext(ctx))
		if err != nil {
			return err
		}
		remaining := batch.LeaseExpiresAt.Sub(now) - 5*time.Second
		if remaining <= 0 {
			return models.ErrFeedLeaseLost
		}
		started := time.Now()
		for offset := 0; offset < len(targets); offset += 256 {
			projections := make([]feed.ProjectionTarget, 0, min(256, len(targets)-offset))
			for i := offset; i < min(offset+256, len(targets)); i++ {
				projections = append(projections, feed.ProjectionTarget{Timeline: targets[i].timeline, Projection: feed.Projection{Invalidate: true, Epoch: epochs[i], BatchID: batch.ID, LeaseGeneration: batch.LeaseGeneration, LeaseToken: batch.LeaseToken, RemainingLease: remaining - time.Since(started)}})
			}
			for _, err := range feed.BatchApplyProjections(ctx, projections) {
				if err != nil {
					return err
				}
			}
		}
		return w.publish(ctx, targets, epochs, nil, event, nil)
	}

	conn := w.DB.WithContext(ctx)
	var status *models.Status
	var notice *models.Notification
	version := max(uint64(1), event.ObjectVersion)
	if event.Kind == models.FeedNotification {
		n := &models.Notification{}
		if err := conn.First(n, event.Metadata.NotificationID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		} else {
			notice = n
		}
	} else {
		s := &models.Status{}
		err := conn.Preload("ToAccounts").Preload("Account").Preload("Tags").First(s, event.ObjectID).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && s.DeletedAt == nil && s.ScheduledAt == nil && (!feed.SkipsTimelines(s) || (batch.TargetKind == "mention" && !s.IsProfileBackfill())) {
			status = s
			version = max(version, s.FeedRevision)
		}
	}
	followers := map[uint64]bool{}
	hiddenReblogs := map[uint64]bool{}
	if status != nil && (batch.TargetKind == "home" || status.Visibility == models.StatusVisibilityPrivate) {
		ids := []uint64{}
		for _, target := range targets {
			if target.account != nil {
				ids = append(ids, target.account.Id)
			}
		}
		if len(ids) > 0 {
			var accepted []uint64
			if err := conn.Model(&models.Follow{}).Where("account_id IN ? AND follow_id = ? AND status = ?", ids, status.AccountId, models.FollowStatusFollowed).Pluck("account_id", &accepted).Error; err != nil {
				return err
			}
			for _, id := range accepted {
				followers[id] = true
			}
			if batch.TargetKind == "home" && status.ReblogId != nil && *status.ReblogId > 0 && (models.FeedOutboxRequired() || conn.Migrator().HasColumn(&models.Follow{}, "properties")) {
				var hidden []uint64
				if err := conn.Model(&models.Follow{}).Where("follows.account_id IN ? AND follows.follow_id = ? AND follows.status = ?", ids, status.AccountId, models.FollowStatusFollowed).Where(models.HiddenReblogsCondition(conn)).Pluck("follows.account_id", &hidden).Error; err != nil {
					return err
				}
				for _, id := range hidden {
					hiddenReblogs[id] = true
				}
			}
		}
	}
	eligibleLists := map[uint64]bool{}
	if status != nil && batch.TargetKind == "list" {
		var err error
		eligibleLists, err = resolveEligibleLists(conn, targets, status)
		if err != nil {
			return err
		}
	}
	hasMedia := false
	if status != nil {
		var count int64
		if err := conn.Model(&models.MediaAttachment{}).Where("status_id = ?", status.Id).Limit(1).Count(&count).Error; err != nil {
			return err
		}
		hasMedia = count > 0
	}
	now, err := models.FeedDatabaseTime(conn)
	if err != nil {
		return err
	}
	remaining := batch.LeaseExpiresAt.Sub(now) - 5*time.Second
	if remaining <= 0 {
		return models.ErrFeedLeaseLost
	}
	started := time.Now()
	projections := make([]feed.ProjectionTarget, 0, len(targets))
	visible := make([]bool, len(targets))
	projectionTargetIndexes := []int{}
	superseded := map[int]bool{}
	for i, target := range targets {
		allowed := eligible(target, status, followers, hiddenReblogs, eligibleLists, hasMedia)
		if notice != nil {
			allowed = target.account != nil && notice.AccountId == target.account.Id
		}
		visible[i] = allowed
		if target.kind == "direct" {
			continue
		}
		p := feed.Projection{Epoch: epochs[i], BatchID: batch.ID, LeaseGeneration: batch.LeaseGeneration, LeaseToken: batch.LeaseToken,
			RemainingLease: remaining - time.Since(started), StatusID: event.ObjectID, ObjectVersion: version, Remove: !allowed}
		if notice != nil {
			p.StatusID = notice.Id
		}
		if status != nil && status.ReblogId != nil {
			p.ReblogID = *status.ReblogId
		}
		if status != nil {
			p.Score = feed.TimelineScore(status)
		}
		if policy, ok := target.timeline.(interface{ AggregateReblogs() bool }); ok {
			p.Aggregate = policy.AggregateReblogs()
		} else if target.kind == "home" {
			p.Aggregate = true
			if target.account.User != nil && target.account.User.Settings != nil {
				p.Aggregate = target.account.User.Settings.GetAggregatesReblogs()
			}
		}
		projectionTargetIndexes = append(projectionTargetIndexes, i)
		projections = append(projections, feed.ProjectionTarget{Timeline: target.timeline, Projection: p})
	}
	// A bounded pipeline covers recipient slots on single Redis or Cluster.
	for offset := 0; offset < len(projections); offset += 256 {
		for i, err := range feed.BatchApplyProjections(ctx, projections[offset:min(offset+256, len(projections))]) {
			if errors.Is(err, feed.ErrSuperseded) {
				superseded[projectionTargetIndexes[offset+i]] = true
				continue
			}
			if err != nil {
				return err
			}
		}
	}
	if len(superseded) > 0 {
		keptTargets := []deliveryTarget{}
		keptEpochs := []string{}
		keptVisible := []bool{}
		for i, target := range targets {
			if !superseded[i] {
				keptTargets = append(keptTargets, target)
				keptEpochs = append(keptEpochs, epochs[i])
				keptVisible = append(keptVisible, visible[i])
			}
		}
		targets, epochs, visible = keptTargets, keptEpochs, keptVisible
	}
	return w.publish(ctx, targets, epochs, visible, event, status)
}

func resolveEligibleLists(conn *gorm.DB, targets []deliveryTarget, status *models.Status) (map[uint64]bool, error) {
	listsByID := map[uint64]*models.List{}
	listIDs := []uint64{}
	for _, target := range targets {
		if target.list != nil && listsByID[target.list.Id] == nil {
			listsByID[target.list.Id] = target.list
			listIDs = append(listIDs, target.list.Id)
		}
	}
	if len(listIDs) == 0 {
		return nil, nil
	}
	var memberListIDs []uint64
	if err := conn.Model(&models.ListAccount{}).Where("list_id IN ? AND account_id = ?", listIDs, status.AccountId).Pluck("list_id", &memberListIDs).Error; err != nil {
		return nil, err
	}
	memberOf := map[uint64]bool{}
	for _, id := range memberListIDs {
		memberOf[id] = true
	}
	isReply := status.InReplyToId != nil && *status.InReplyToId != 0
	replyToAccount := uint64(0)
	if status.InReplyToAccountId != nil {
		replyToAccount = *status.InReplyToAccountId
	}
	replyTargetMemberOf := map[uint64]bool{}
	ownerFollowsReplyTarget := map[uint64]bool{}
	if isReply && replyToAccount != 0 && len(memberOf) > 0 {
		var replyMemberLists []uint64
		if err := conn.Model(&models.ListAccount{}).Where("list_id IN ? AND account_id = ?", memberListIDs, replyToAccount).Pluck("list_id", &replyMemberLists).Error; err != nil {
			return nil, err
		}
		for _, id := range replyMemberLists {
			replyTargetMemberOf[id] = true
		}
		ownerIDs := []uint64{}
		for _, id := range memberListIDs {
			if l := listsByID[id]; l != nil && l.RepliesPolicy == models.ListRepliesPolicyFollowed {
				ownerIDs = append(ownerIDs, l.AccountId)
			}
		}
		if len(ownerIDs) > 0 {
			var followedOwners []uint64
			if err := conn.Model(&models.Follow{}).Where("account_id IN ? AND follow_id = ? AND status = ?", ownerIDs, replyToAccount, models.FollowStatusFollowed).Pluck("account_id", &followedOwners).Error; err != nil {
				return nil, err
			}
			for _, id := range followedOwners {
				ownerFollowsReplyTarget[id] = true
			}
		}
	}
	taggedListOf := map[uint64]bool{}
	blockedOwners := map[uint64]bool{}
	if len(status.Tags) > 0 {
		tagIDs := make([]uint64, 0, len(status.Tags))
		for _, item := range status.Tags {
			if item.Id != 0 {
				tagIDs = append(tagIDs, item.Id)
			}
		}
		if len(tagIDs) > 0 {
			var matched []uint64
			if err := conn.Model(&models.ListHashtag{}).Where("list_id IN ? AND tag_id IN ?", listIDs, tagIDs).Pluck("list_id", &matched).Error; err != nil {
				return nil, err
			}
			ownerIDs := []uint64{}
			for _, id := range matched {
				taggedListOf[id] = true
				if l := listsByID[id]; l != nil {
					ownerIDs = append(ownerIDs, l.AccountId)
				}
			}
			if len(ownerIDs) > 0 {
				var blocked []uint64
				q := conn.Model(&models.Block{}).Where(
					"(type = ? AND ((account_id IN ? AND target_account_id = ?) OR (account_id = ? AND target_account_id IN ?)))",
					models.BlockTypeAccount, ownerIDs, status.AccountId, status.AccountId, ownerIDs,
				)
				if err := q.Select("CASE WHEN account_id = ? THEN target_account_id ELSE account_id END", status.AccountId).Pluck("owner_id", &blocked).Error; err != nil {
					return nil, err
				}
				for _, id := range blocked {
					blockedOwners[id] = true
				}
				if status.Domain != nil && *status.Domain != "" {
					var domainBlocked []uint64
					if err := conn.Model(&models.Block{}).Where("type = ? AND account_id IN ? AND target_domain = ?", models.BlockTypeDomain, ownerIDs, *status.Domain).Pluck("account_id", &domainBlocked).Error; err != nil {
						return nil, err
					}
					for _, id := range domainBlocked {
						blockedOwners[id] = true
					}
				}
			}
		}
	}
	out := make(map[uint64]bool, len(listIDs))
	for id, l := range listsByID {
		if memberOf[id] {
			switch l.RepliesPolicy {
			case models.ListRepliesPolicyNone:
				if !isReply {
					out[id] = true
				}
			case models.ListRepliesPolicyFollowed:
				if !isReply || ownerFollowsReplyTarget[l.AccountId] {
					out[id] = true
				}
			default:
				if !isReply || replyTargetMemberOf[id] {
					out[id] = true
				}
			}
		}
		if !out[id] && taggedListOf[id] && !blockedOwners[l.AccountId] && (l.RepliesPolicy != models.ListRepliesPolicyNone || !isReply) {
			out[id] = true
		}
	}
	return out, nil
}

// This is candidate membership, not the final authorization check. HTTP and
// streaming reload SQL and apply blocks, bans, quotes and reblog authorization.
func eligible(target deliveryTarget, status *models.Status, followers, hiddenReblogs, eligibleLists map[uint64]bool, hasMedia bool) bool {
	if status == nil || status.Account == nil || status.Account.IsBan {
		return false
	}
	mentioned := false
	if target.account != nil {
		for _, account := range status.ToAccounts {
			if account.Id == target.account.Id {
				mentioned = true
				break
			}
		}
	}
	switch target.kind {
	case "home":
		if target.account == nil {
			return false
		}
		if target.account.Id == status.AccountId {
			return true
		}
		if status.ReblogId != nil && *status.ReblogId > 0 && hiddenReblogs[target.account.Id] {
			return false
		}
		if status.Visibility == models.StatusVisibilityDirect {
			return mentioned
		}
		return mentioned || followers[target.account.Id]
	case "mention":
		return mentioned
	case "direct":
		return status.Visibility == models.StatusVisibilityDirect && target.account != nil && (mentioned || target.account.Id == status.AccountId)
	case "public":
		t := target.timeline.(*public.PublicTimeline)
		return status.Visibility == models.StatusVisibilityPublic && (!t.OnlyLocal || status.Domain == nil) && (!t.OnlyRemote || status.Domain != nil) && (!t.OnlyMedia || hasMedia)
	case "tag":
		t := target.timeline.(*tag.TagTimeline)
		found := false
		for _, item := range status.Tags {
			if models.TagKey(item.Name) == models.TagKey(t.Tag) {
				found = true
				break
			}
		}
		return found && (!t.OnlyLocal || status.Domain == nil) && (!t.OnlyRemote || status.Domain != nil) && (!t.OnlyMedia || hasMedia)
	case "list":
		t := target.timeline.(*list.ListTimeline)
		if target.list == nil || !eligibleLists[target.list.Id] {
			return false
		}
		return (status.Visibility == models.StatusVisibilityPublic || status.Visibility == models.StatusVisibilityUnlisted) && (!t.OnlyLocal || status.Domain == nil) && (!t.OnlyRemote || status.Domain != nil) && (!t.OnlyMedia || hasMedia)
	}
	return false
}

func (w *Worker) publish(ctx context.Context, targets []deliveryTarget, epochs []string, visible []bool, event *models.FeedEvent, status *models.Status) error {
	if w.Options.Shadow {
		return nil
	}
	// Only identifiers cross Redis; third-party streams rehydrate and authorize
	// immediately before serialization. The webpage sees only opaque revisions.
	client := db.GetUniversalRedis()
	_, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for i, target := range targets {
			if event.Kind == models.FeedStatusCreate && i < len(visible) && !visible[i] {
				continue
			}
			epoch := ""
			if i < len(epochs) {
				epoch = epochs[i]
			}
			hint, _ := json.Marshal(map[string]any{"type": "feed.changed", "payload": feed.ChangeHint{Epoch: epoch, Revision: strconv.FormatUint(event.ID, 10)}})
			pipe.Publish(ctx, feed.ChangedChannel(target.timeline), hint)
			if event.Kind == models.FeedInvalidate {
				continue
			}
			kind := "update"
			payload := map[string]any{"id": event.ObjectID}
			if event.Kind == models.FeedStatusRefresh {
				kind = "status.update"
			}
			if !visible[i] || event.Kind == models.FeedStatusDelete {
				kind = "delete"
			}
			if target.kind == "notification" {
				if !visible[i] {
					continue
				}
				kind = "notification"
				payload = map[string]any{"id": strconv.FormatUint(event.Metadata.NotificationID, 10)}
			}
			if target.kind == "direct" {
				if event.Kind == models.FeedStatusDelete {
					kind = "delete"
					payload = map[string]any{"id": event.ObjectID}
				} else {
					if !visible[i] || status == nil || status.ConversationId == nil {
						continue
					}
					kind = "conversation"
					payload = map[string]any{"id": *status.ConversationId}
				}
			}
			// Private/unlisted/deleted payload identifiers never go to a public/hashtag channel.
			if target.kind == "public" || target.kind == "tag" {
				if kind == "delete" {
					if event.Metadata.Visibility != models.StatusVisibilityPublic {
						continue
					}
				} else if status == nil || status.Visibility != models.StatusVisibilityPublic {
					continue
				}
			}
			if target.kind == "list" && kind == "delete" && event.Metadata.Visibility != models.StatusVisibilityPublic && event.Metadata.Visibility != models.StatusVisibilityUnlisted {
				continue
			}
			wire, _ := json.Marshal(map[string]any{"type": kind, "payload": payload})
			pipe.Publish(ctx, feed.GetRedisChannelName(target.timeline), wire)
		}
		return nil
	})
	return err
}
