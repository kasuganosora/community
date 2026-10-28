package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/kasuganosora/community/systemsettings"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/htmlsafe"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"github.com/kasuganosora/community/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ---------------------------------------------------------------------------
// Outbound: local polls are federated as Mastodon-compatible Questions.
// ---------------------------------------------------------------------------

// statusPoll returns the status' poll, loading it when the caller did not
// preload it (collections, the object endpoint).
func statusPoll(status *models.Status) *models.Poll {
	if status.Poll != nil {
		return status.Poll
	}
	if status.Id == 0 || db.GetDB() == nil {
		return nil
	}
	var poll models.Poll
	if err := db.GetDB().Preload("Options").Where("status_id = ?", status.Id).First(&poll).Error; err != nil {
		return nil
	}
	status.Poll = &poll
	return &poll
}

// pollVotersCount counts distinct voters; remote aggregates are kept when
// larger (a remote poll stores only the author's reported totals).
func pollVotersCount(poll *models.Poll) uint64 {
	var voters int64
	if poll.Id != 0 && db.GetDB() != nil {
		_ = db.GetDB().Model(&models.PollOptionVote{}).Where("poll_id = ?", poll.Id).Distinct("account_id").Count(&voters).Error
	}
	count := uint64(voters) //nolint:gosec // non-negative count
	if remote := poll.RemoteVotersCount(); remote > count {
		count = remote
	}
	if count == 0 && !poll.Multiple {
		for _, o := range poll.Options {
			count += o.Votes
		}
	}
	return count
}

// applyQuestionFields turns a Note map into a Mastodon-style Question.
func applyQuestionFields(note map[string]any, poll *models.Poll, now time.Time) {
	note["type"] = "Question"
	options := append(models.PollOptions{}, poll.Options...).SortByIndex()
	choices := make([]map[string]any, 0, len(options))
	for _, o := range options {
		choices = append(choices, map[string]any{
			"type":    "Note",
			"name":    o.Content,
			"replies": map[string]any{"type": "Collection", "totalItems": o.Votes},
		})
	}
	if poll.Multiple {
		note["anyOf"] = choices
	} else {
		note["oneOf"] = choices
	}
	if poll.ExpiresAt != nil {
		end := poll.ExpiresAt.UTC().Format(time.RFC3339)
		note["endTime"] = end
		if poll.IsClosed(now) {
			note["closed"] = end
		}
	}
	note["votersCount"] = pollVotersCount(poll)
	note["@context"] = questionContext(note["@context"])
}

// questionContext adds toot:votersCount (Mastodon's term) to a context.
func questionContext(current any) []any {
	var ctx []any
	switch c := current.(type) {
	case []any:
		ctx = append(ctx, c...)
	case string:
		ctx = []any{c}
	default:
		ctx = []any{"https://www.w3.org/ns/activitystreams"}
	}
	for i, item := range ctx {
		if m, ok := item.(map[string]any); ok {
			copyMap := make(map[string]any, len(m)+2)
			for k, v := range m {
				copyMap[k] = v
			}
			copyMap["toot"] = "http://joinmastodon.org/ns#"
			copyMap["votersCount"] = "toot:votersCount"
			ctx[i] = copyMap
			return ctx
		}
	}
	return append(ctx, map[string]any{"toot": "http://joinmastodon.org/ns#", "votersCount": "toot:votersCount"})
}

// ---------------------------------------------------------------------------
// Inbound: remote Questions (Mastodon, Misskey, Pleroma...).
// ---------------------------------------------------------------------------

func isStatusObjectType(t string) bool {
	return t == "Note" || t == "Question" || t == "Article" || t == "Page"
}

// questionPoll parses a remote Question into a poll (nil when it has none).
func questionPoll(object map[string]any, accountID uint64) *models.Poll {
	poll, _ := parseQuestion(object, accountID)
	return poll
}

// parseQuestion also reports, per option, whether the object carried a tally.
func parseQuestion(object map[string]any, accountID uint64) (*models.Poll, []bool) {
	if t, _ := object["type"].(string); t != "Question" {
		return nil, nil
	}
	multiple := false
	raw, ok := object["oneOf"].([]any)
	if !ok || len(raw) == 0 {
		raw, _ = object["anyOf"].([]any)
		multiple = true
	}
	if len(raw) == 0 {
		return nil, nil
	}
	var known []bool
	poll := &models.Poll{AccountId: accountID, Multiple: multiple, Properties: map[string]any{"remote": true}}
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		name = strings.TrimSpace(htmlsafe.StripTags(name))
		if name == "" || len(poll.Options) >= 255 {
			continue
		}
		if utf8.RuneCountInString(name) > 500 {
			name = string([]rune(name)[:500])
		}
		votes, ok := replyTotal(m)
		known = append(known, ok)
		poll.Options = append(poll.Options, &models.PollOption{Index: uint8(len(poll.Options)), Content: name, Votes: votes}) //nolint:gosec // bounded above
	}
	if len(poll.Options) == 0 {
		return nil, nil
	}
	applyQuestionState(poll, object)
	return poll, known
}

// replyTotal reads an option's replies.totalItems. known is false when the
// option carries no tally at all (some servers omit it in Updates or for
// anyOf), so callers must not treat that as zero votes.
func replyTotal(option map[string]any) (votes uint64, known bool) {
	replies, ok := option["replies"].(map[string]any)
	if !ok {
		return 0, false
	}
	switch n := replies["totalItems"].(type) {
	case float64:
		if n > 0 {
			return uint64(n), true
		}
		return 0, true
	case string:
		if v, err := strconv.ParseUint(strings.TrimSpace(n), 10, 64); err == nil {
			return v, true
		}
	}
	return 0, false
}

// applyQuestionState copies endTime/closed/votersCount onto a poll.
func applyQuestionState(poll *models.Poll, object map[string]any) {
	if poll.Properties == nil {
		poll.Properties = map[string]any{}
	}
	end := questionTime(object["endTime"])
	closedAt := questionTime(object["closed"])
	if closed, ok := object["closed"].(bool); ok && closed && closedAt == nil {
		now := time.Now()
		closedAt = &now
	}
	if closedAt != nil {
		poll.Properties["closed"] = true
		if end == nil || closedAt.Before(*end) {
			end = closedAt
		}
	}
	if end != nil {
		poll.ExpiresAt = end
	}
	if n, ok := object["votersCount"].(float64); ok && n >= 0 {
		poll.Properties["voters_count"] = n
	}
}

func questionTime(v any) *time.Time {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	return utils.ParseRFC3339(s)
}

// updateRemotePoll applies an Update(Question): tallies, endTime, closed.
func updateRemotePoll(tx *gorm.DB, statusID, accountID uint64, object map[string]any) error {
	parsed, known := parseQuestion(object, accountID)
	if parsed == nil {
		return nil
	}
	var poll models.Poll
	// Lock order polls -> poll_options, like votes and status deletion.
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Options").Where("status_id = ?", statusID).First(&poll).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		parsed.StatusId = statusID
		return tx.Create(parsed).Error
	}
	if err != nil {
		return err
	}
	byName := map[string]*models.PollOption{}
	byIndex := map[uint8]*models.PollOption{}
	for _, o := range poll.Options {
		byName[strings.TrimSpace(o.Content)] = o
		byIndex[o.Index] = o
	}
	// Local accounts' votes on this remote poll are known exactly; a remote
	// tally can lag (or be missing) but never be lower than them.
	type localCount struct {
		OptionID uint64
		N        uint64
	}
	var locals []localCount
	if err := tx.Table("poll_option_votes").Select("poll_option_votes.option_id AS option_id, COUNT(*) AS n").
		Joins("JOIN accounts ON accounts.id = poll_option_votes.account_id AND accounts.domain IS NULL").
		Where("poll_option_votes.poll_id = ?", poll.Id).Group("poll_option_votes.option_id").Scan(&locals).Error; err != nil {
		return err
	}
	localVotes := map[uint64]uint64{}
	for _, l := range locals {
		localVotes[l.OptionID] = l.N
	}
	sameShape := len(parsed.Options) == len(poll.Options)
	for i, o := range parsed.Options {
		existing := byName[strings.TrimSpace(o.Content)]
		if existing == nil && sameShape {
			existing = byIndex[o.Index]
		}
		if existing != nil {
			if !known[i] {
				continue
			}
			if floor := localVotes[existing.Id]; o.Votes < floor {
				o.Votes = floor
			}
			if existing.Votes != o.Votes {
				if err := tx.Model(&models.PollOption{}).Where("id = ?", existing.Id).Update("votes", o.Votes).Error; err != nil {
					return err
				}
			}
			continue
		}
		o.PollId = poll.Id
		if err := tx.Create(o).Error; err != nil {
			return err
		}
	}
	props := poll.Properties
	if props == nil {
		props = map[string]any{}
	}
	for k, v := range parsed.Properties {
		props[k] = v
	}
	// Struct updates run the JSON serializer of Properties on every driver;
	// a map update passes the raw map, which some drivers reject.
	cols := []string{"properties", "multiple"}
	if parsed.ExpiresAt != nil {
		cols = append(cols, "expires_at")
	}
	return tx.Model(&models.Poll{Id: poll.Id}).Select(cols).Updates(&models.Poll{Properties: props, Multiple: parsed.Multiple, ExpiresAt: parsed.ExpiresAt}).Error
}

// ---------------------------------------------------------------------------
// Inbound votes on local polls: Create(Note{name, inReplyTo: question}).
// ---------------------------------------------------------------------------

// isPollVoteNote reports a Mastodon/Misskey vote: a Note with a name and
// inReplyTo, and no content of its own.
func isPollVoteNote(object map[string]any) bool {
	name, _ := object["name"].(string)
	inReplyTo, _ := object["inReplyTo"].(string)
	if strings.TrimSpace(name) == "" || inReplyTo == "" {
		return false
	}
	content, _ := object["content"].(string)
	return strings.TrimSpace(htmlsafe.StripTags(content)) == "" || strings.TrimSpace(htmlsafe.StripTags(content)) == strings.TrimSpace(name)
}

var errNotLocalPoll = errors.New("not a vote on a local poll")

// recordRemoteVote counts one remote vote. It returns errNotLocalPoll when
// inReplyTo is not an open local poll so the caller can treat the Note as a
// normal reply. Duplicate or invalid votes are ignored (nil error).
func recordRemoteVote(ctx context.Context, voter *models.Account, object map[string]any) error {
	inReplyTo, _ := object["inReplyTo"].(string)
	name, _ := object["name"].(string)
	name = strings.TrimSpace(name)
	parent, err := findInboundParent(ctx, inReplyTo)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errNotLocalPoll
		}
		return err
	}
	if !parent.IsLocal() {
		return errNotLocalPoll
	}
	tx := activityDB(ctx)
	var poll models.Poll
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("status_id = ?", parent.Id).First(&poll).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errNotLocalPoll
		}
		return err
	}
	if poll.IsClosed(time.Now()) {
		return nil
	}
	visible, err := parent.CanViewEx(tx, voter)
	if err != nil || !visible {
		return err
	}
	var option models.PollOption
	if err := tx.Where("poll_id = ? AND content = ?", poll.Id, name).First(&option).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var existing models.PollOptionVotes
	if err := tx.Where("poll_id = ? AND account_id = ?", poll.Id, voter.Id).Find(&existing).Error; err != nil {
		return err
	}
	if len(existing) > 0 && (!poll.Multiple || len(existing.FilterOptionId(option.Id)) > 0) {
		return nil
	}
	if err := tx.Create(&models.PollOptionVote{PollId: poll.Id, OptionId: option.Id, AccountId: voter.Id}).Error; err != nil {
		return err
	}
	if err := tx.Model(&models.PollOption{}).Where("id = ?", option.Id).UpdateColumn("votes", gorm.Expr("votes + 1")).Error; err != nil {
		return err
	}
	return models.QueuePollUpdate(tx, parent.Id, false)
}

// ---------------------------------------------------------------------------
// Outbound votes on remote polls.
// ---------------------------------------------------------------------------

func pollVoteActivities(voter *models.Account, pollStatus *models.Status, choices []string, baseURL, activityID string) []map[string]any {
	actor := accountActorURI(voter, baseURL)
	author := pollStatus.Account.OriginId
	question := statusObjectURI(pollStatus, pollStatus.Account, baseURL)
	out := make([]map[string]any, 0, len(choices))
	for i, choice := range choices {
		id := activityID + "/votes/" + strconv.Itoa(i)
		out = append(out, map[string]any{
			"@context": "https://www.w3.org/ns/activitystreams",
			"id":       id + "/activity",
			"type":     "Create",
			"actor":    actor,
			"to":       []string{author},
			"object": map[string]any{
				"id":           id,
				"type":         "Note",
				"name":         choice,
				"attributedTo": actor,
				"to":           []string{author},
				"inReplyTo":    question,
			},
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Poll close worker: notifications + closing Update, exactly once per poll.
// ---------------------------------------------------------------------------

// PollCloseWorker scans polls whose voting ended and have not been handled.
type PollCloseWorker struct {
	stop chan struct{}
	done chan struct{}
}

func StartPollCloseWorker() *PollCloseWorker {
	w := &PollCloseWorker{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(w.done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if err := ProcessClosedPolls(context.Background(), time.Now(), 100); err != nil {
				log.Warn("Poll close worker: %v", err)
			}
			select {
			case <-w.stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return w
}

func (w *PollCloseWorker) Stop() { close(w.stop); <-w.done }

// ProcessClosedPolls handles up to limit ended polls. Each poll is claimed by
// setting closed_notified_at in its own transaction, so a poll is notified
// once even with several workers or retries.
func ProcessClosedPolls(ctx context.Context, now time.Time, limit int) error {
	conn := db.GetDB().WithContext(ctx)
	var polls []models.Poll
	if err := conn.Where("closed_notified_at IS NULL AND expires_at IS NOT NULL AND expires_at <= ?", now).Order("expires_at ASC").Limit(limit).Find(&polls).Error; err != nil {
		return err
	}
	for i := range polls {
		if err := conn.Transaction(func(tx *gorm.DB) error { return closePoll(tx, &polls[i], now) }); err != nil {
			log.Warn("Poll %d close: %v", polls[i].Id, err)
		}
	}
	return nil
}

func closePoll(tx *gorm.DB, poll *models.Poll, now time.Time) error {
	claim := tx.Model(&models.Poll{}).Where("id = ? AND closed_notified_at IS NULL", poll.Id).Update("closed_notified_at", now)
	if claim.Error != nil || claim.RowsAffected != 1 {
		return claim.Error
	}
	var status models.Status
	if err := tx.Preload("Account").First(&status, poll.StatusId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if status.Account == nil {
		return nil
	}
	recipients := []uint64{}
	if status.Account.Domain == nil {
		recipients = append(recipients, status.Account.Id)
	}
	var voters []uint64
	if err := tx.Table("poll_option_votes").Joins("JOIN accounts ON accounts.id = poll_option_votes.account_id AND accounts.domain IS NULL AND accounts.deleted_at IS NULL").
		Where("poll_option_votes.poll_id = ?", poll.Id).Distinct().Pluck("poll_option_votes.account_id", &voters).Error; err != nil {
		return err
	}
	recipients = append(recipients, voters...)
	seen := map[uint64]bool{}
	for _, id := range recipients {
		if seen[id] {
			continue
		}
		seen[id] = true
		var exists int64
		if err := tx.Model(&models.Notification{}).Where("account_id = ? AND status_id = ? AND type = ?", id, status.Id, models.NotificationTypePoll).Count(&exists).Error; err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		if err := tx.Create(&models.Notification{AccountId: id, FromAccountId: status.Account.Id, StatusId: status.Id, Type: models.NotificationTypePoll}).Error; err != nil {
			return err
		}
	}
	if status.Account.Domain == nil && status.IsLocal() {
		return models.QueuePollUpdate(tx, status.Id, true)
	}
	return nil
}

// pollVoteDeliveries expands a "PollVote" outbox event into one Create(Note)
// delivery per choice, addressed only to the remote poll author.
func pollVoteDeliveries(ctx context.Context, e *models.FederationOutbox, voter *models.Account) ([]Delivery, error) {
	var payload models.PollVotePayload
	if err := json.Unmarshal([]byte(e.Payload), &payload); err != nil {
		return nil, err
	}
	var status models.Status
	if err := db.GetDB().Preload("Account").First(&status, payload.StatusID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if status.Account == nil || status.Account.Domain == nil {
		return nil, nil
	}
	allowed, err := outboundRecipientAllowed(nil, voter, status.Account)
	if err != nil || !allowed {
		return nil, err
	}
	base := systemsettings.GetBaseURL()
	activityID := accountActorURI(voter, base) + "/activities/" + strconv.FormatUint(e.ID, 10)
	inbox, err := remoteActorInbox(ctx, status.Account)
	if err != nil {
		// The author is the only recipient. Record the miss and finish the
		// event so a 502 does not retry the outbox row with zero deliveries.
		return []Delivery{failedRecipientDelivery(e, activityID, e.Payload, status.Account, &status, status.Account.OriginId, err)}, nil
	}
	auth, err := json.Marshal(DeliveryAuthorization{AccountID: voter.Id, RecipientIDs: []uint64{status.Account.Id}, Kind: "PollVote"})
	if err != nil {
		return nil, err
	}
	var deliveries []Delivery
	for _, activity := range pollVoteActivities(voter, &status, payload.Choices, base, activityID) {
		data, err := json.Marshal(activity)
		if err != nil {
			return nil, err
		}
		deliveries = append(deliveries, Delivery{ActivityID: activity["id"].(string), TargetInbox: inbox, Payload: string(data), Authorization: string(auth), Status: DeliveryStatusPending})
	}
	return deliveries, nil
}
