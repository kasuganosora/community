package timelines

import (
	"github.com/kasuganosora/community/feed"
	"github.com/kasuganosora/community/systemsettings"
)

type TimelineRequest struct {
	MaxId   uint64 `query:"max_id"`
	MinId   uint64 `query:"min_id"`
	SinceId uint64 `query:"since_id"`
	Limit   uint64 `query:"limit"`
}

func (req *TimelineRequest) ToFeedListParams() *feed.ListParam {

	// 如果没有传limit，就默认为20
	if req.Limit == 0 {
		req.Limit = uint64(systemsettings.GetDefaultPageSize())
	}

	return &feed.ListParam{
		MaxId:   req.MaxId,
		MinId:   req.MinId,
		SinceId: req.SinceId,
		Limit:   req.Limit,
		Other:   make(map[string]any),
	}
}
