package discovery

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var feedRequests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "community_discovery_feed_requests_total", Help: "Recommendation service reads by actual mode and outcome."}, []string{"mode", "outcome"})
var feedReadDuration = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "community_discovery_feed_service_duration_seconds", Help: "Recommendation service latency, excluding HTTP serialization.", Buckets: []float64{.01, .025, .05, .1, .15, .3, .5, 1, 2}})
var feedCandidateRows = prometheus.NewGauge(prometheus.GaugeOpts{Name: "community_discovery_feed_pool_candidates", Help: "Candidate count in the last pool published by this process."})
var feedPoolTruncation = prometheus.NewCounter(prometheus.CounterOpts{Name: "community_discovery_feed_pool_truncated_total", Help: "Pool refreshes that hit the 2000-row sample budget."})
var feedRefreshOutcome = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "community_discovery_feed_refresh_total", Help: "Bounded candidate refresh outcomes."}, []string{"outcome"})

func init() {
	prometheus.MustRegister(feedRequests, feedReadDuration, feedCandidateRows, feedPoolTruncation, feedRefreshOutcome)
}

func observeFeedRead(start time.Time, page Page, err error) {
	mode := page.Mode
	if mode == "" {
		mode = "unavailable"
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
	}
	if errors.Is(err, ErrResetRequired) {
		outcome = "reset_required"
	}
	feedRequests.WithLabelValues(mode, outcome).Inc()
	feedReadDuration.Observe(time.Since(start).Seconds())
}
