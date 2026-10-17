package activitypub

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/log"
	"github.com/kasuganosora/community/models"
	"gorm.io/gorm"
)

// RefetchRemotePoll fetches a remote status' Question again (signed as the
// instance actor) and applies its current tallies, end time and closed
// state. It repairs polls whose stored counts went wrong.
func RefetchRemotePoll(ctx context.Context, statusID uint64) error {
	var status models.Status
	if err := db.GetDB().WithContext(ctx).Preload("Account").First(&status, statusID).Error; err != nil {
		return err
	}
	if status.Account == nil || status.Account.Domain == nil || status.OriginUri == nil || *status.OriginUri == "" {
		return errors.New("not a remote status")
	}
	if err := validateFederationURI(*status.OriginUri); err != nil {
		return err
	}
	client, err := refetchPollClient(ctx)
	if err != nil || client == nil {
		client = NewFetchClient()
	}
	obj, err := client.FetchObject(ctx, *status.OriginUri)
	if err != nil {
		return fmt.Errorf("fetching %s: %w", *status.OriginUri, err)
	}
	if id, _ := obj["id"].(string); id != *status.OriginUri {
		return errors.New("fetched object id does not match the status")
	}
	if firstActorID(obj["attributedTo"]) != status.Account.OriginId {
		return errors.New("fetched object author does not match the status")
	}
	if t, _ := obj["type"].(string); t != "Question" {
		return errors.New("remote object is not a poll")
	}
	return db.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return updateRemotePoll(tx, status.Id, status.AccountId, obj)
	})
}

var refetchPollClient = func(ctx context.Context) (*Client, error) { return instanceActorSigningClient(ctx) }

// pollRepairs throttles automatic repairs: one attempt per status per hour.
var pollRepairs = struct {
	sync.Mutex
	last map[uint64]time.Time
}{last: map[uint64]time.Time{}}

// startPollRepair runs repairs; tests replace it.
var startPollRepair = func(f func()) { go f() }

// pollLooksInconsistent reports a closed remote poll whose option tallies
// add up to fewer votes than were counted (votesCount includes the votes of
// local accounts), e.g. tallies zeroed by a bad Update.
func pollLooksInconsistent(poll *models.Poll, votesCount uint64, now time.Time) bool {
	if poll == nil || !poll.IsClosed(now) {
		return false
	}
	if remote, _ := poll.Properties["remote"].(bool); !remote {
		return false
	}
	var sum uint64
	for _, o := range poll.Options {
		sum += o.Votes
	}
	return votesCount > sum
}

// MaybeRepairRemotePoll schedules a background refetch of a viewed remote
// poll that looks inconsistent. It never blocks the caller.
func MaybeRepairRemotePoll(poll *models.Poll, votesCount uint64) {
	now := time.Now()
	if !pollLooksInconsistent(poll, votesCount, now) {
		return
	}
	pollRepairs.Lock()
	if t, ok := pollRepairs.last[poll.StatusId]; ok && now.Sub(t) < time.Hour {
		pollRepairs.Unlock()
		return
	}
	if len(pollRepairs.last) > 10000 {
		pollRepairs.last = map[uint64]time.Time{}
	}
	pollRepairs.last[poll.StatusId] = now
	pollRepairs.Unlock()
	statusID := poll.StatusId
	startPollRepair(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := RefetchRemotePoll(ctx, statusID); err != nil {
			log.Info("poll repair of status %d: %v", statusID, err)
		}
	})
}
