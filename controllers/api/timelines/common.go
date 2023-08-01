package timelines

type TimeLineRequest struct {
	MaxId   uint64 `query:"max_id"`
	MinId   uint64 `query:"min_id"`
	SinceId uint64 `query:"since_id"`
	Limit   uint64 `query:"limit"`
}
