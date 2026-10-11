package feed

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Labels describe bounded outcomes only; identities and content never appear.
var (
	cacheReads         = promauto.NewCounterVec(prometheus.CounterOpts{Name: "community_feed_cache_reads_total", Help: "Candidate reads by ready, incomplete, error or empty outcome."}, []string{"outcome"})
	sqlFallbacks       = promauto.NewCounterVec(prometheus.CounterOpts{Name: "community_feed_sql_fallback_total", Help: "SQL candidate fallback admission by accepted or busy outcome."}, []string{"outcome"})
	rebuildOutcomes    = promauto.NewCounterVec(prometheus.CounterOpts{Name: "community_feed_rebuild_total", Help: "Coalesced cache rebuild attempts by success or error outcome."}, []string{"outcome"})
	filteredCandidates = promauto.NewCounter(prometheus.CounterOpts{Name: "community_feed_filtered_candidates_total", Help: "Candidates removed by current membership, mute, visibility or response filters."})
	redisBatchDuration = promauto.NewHistogram(prometheus.HistogramOpts{Name: "community_feed_redis_pipeline_seconds", Help: "Feed Redis pipeline duration including retries.", Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 5}})
	redisBatchErrors   = promauto.NewCounter(prometheus.CounterOpts{Name: "community_feed_redis_pipeline_errors_total", Help: "Failed Feed Redis pipelines; SQL tasks are not acknowledged."})
)
