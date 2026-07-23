package timelines

import (
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/systemsettings"
)

type TimelineRequest struct {
	MaxId   uint64 `form:"max_id"`
	MinId   uint64 `form:"min_id"`
	SinceId uint64 `form:"since_id"`
	Limit   uint64 `form:"limit"`
}

func (req *TimelineRequest) ToFeedListParams() *feed.ListParam {
	// 如果没有传limit，就默认为20
	if req.Limit == 0 {
		req.Limit = uint64(systemsettings.GetDefaultPageSize()) //nolint:gosec // G115: reviewed and safe
	}

	if req.Limit > 80 {
		req.Limit = 80
	}

	return &feed.ListParam{
		MaxId:   req.MaxId,
		MinId:   req.MinId,
		SinceId: req.SinceId,
		Limit:   req.Limit,
		Other:   make(map[string]any),
	}
}
