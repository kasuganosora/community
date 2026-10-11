package streaming

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/kasuganosora/community/db"
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/feed/home"
	listFeed "github.com/kasuganosora/community/feed/list"
	"github.com/kasuganosora/community/feed/public"
	"github.com/kasuganosora/community/feed/tag"
	"github.com/kasuganosora/community/models"
)

func isStatusStreamEvent(payload string) bool {
	var envelope struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(payload), &envelope) != nil {
		return false
	}
	return envelope.Type == "update" || envelope.Type == "status.update" || envelope.Type == "delete"
}

// Use the HTTP timeline's exact SQL predicates rather than duplicating them in
// a second streaming authorization implementation. Exclusive bounds describe
// one integer ID, even at uint64's endpoints; no history scan or Redis rebuild.
func currentTimelineStatus(account *models.Account, id uint64, channel string) (models.Statuses, string, error) {
	stream, kind, err := streamForChannel(account, channel)
	if err != nil {
		return nil, "", err
	}
	if id == 0 {
		return nil, "", errEventNotVisible
	}
	params := &feed.ListParam{Viewer: account, Limit: 1, MinId: id - 1, Other: map[string]any{"database_only": true}}
	if id < ^uint64(0) {
		params.MaxId = id + 1
	}
	parts := strings.Split(stream[0], ":")
	for _, part := range parts[1:] {
		switch part {
		case "local", "remote":
			params.Other[part] = true
		case "media":
			params.Other["only_media"] = true
		}
	}
	contextName := "public"
	if kind == "home" {
		contextName = "home"
	}
	if kind == "list" {
		contextName = "home"
	} // List HTTP handlers use the home filter context.
	ctx, cancel := context.WithTimeout(models.WithFilterContext(context.Background(), contextName), 5*time.Second)
	defer cancel()
	params.Context = ctx
	var statuses models.Statuses
	switch kind {
	case "home":
		statuses, err = home.GetTimeLineContent(account, params)
	case "public":
		if media, _ := params.Other["only_media"].(bool); media {
			statuses, err = public.GetTimelineContentMedia(params)
		} else {
			statuses, err = public.GetTimelineContentAll(params)
		}
	case "hashtag":
		statuses, err = tag.GetTimelineContent(stream[1], account, params)
	case "list":
		listID, parseErr := strconv.ParseUint(stream[1], 10, 64)
		if parseErr != nil {
			return nil, "", parseErr
		}
		var current models.List
		if err = db.GetDB().WithContext(ctx).First(&current, listID).Error; err != nil {
			return nil, "", err
		}
		// Streaming activity does not rewrite last_viewed_at for every incoming event.
		now := time.Now()
		current.LastViewedAt = &now
		statuses, err = listFeed.GetTimelineContent(&current, params)
	default:
		return nil, "", errEventNotVisible
	}
	if err != nil {
		return nil, "", err
	}
	if len(statuses) != 1 || statuses[0].Id != id {
		return nil, "", errEventNotVisible
	}
	return statuses, contextName, nil
}
