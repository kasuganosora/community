package feed

import (
	"context"
	"encoding/json"

	"github.com/kasuganosora/community/db"
)

// ChangeHint is deliberately free of post IDs, content and recipient identity.
// It is an invalidation hint: HTTP remains the authority after every reconnect.
type ChangeHint struct {
	Epoch    string `json:"epoch"`
	Revision string `json:"revision"`
}

func ChangedChannel(timeline Timeline) string { return "changed:" + TimelineKey(timeline) }

func PublishChanged(ctx context.Context, timeline Timeline, epoch, revision string) error {
	data, err := json.Marshal(struct {
		Type    string     `json:"type"`
		Payload ChangeHint `json:"payload"`
	}{"feed.changed", ChangeHint{epoch, revision}})
	if err != nil {
		return err
	}
	return db.GetUniversalRedis().Publish(ctx, ChangedChannel(timeline), data).Err()
}
