package main

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStandaloneWorkerMetricsHasNoApplicationRoutes(t *testing.T) {
	server, err := startWorkerMetrics("127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { stopWorkerMetrics(server) })
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get("http://" + server.Addr + "/metrics")
	require.NoError(t, err)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode)
	require.Contains(t, string(body), "community_feed_filtered_candidates_total")
	other, err := client.Get("http://" + server.Addr + "/api/v1/timelines/public")
	require.NoError(t, err)
	defer other.Body.Close()
	require.Equal(t, 404, other.StatusCode)
	stopWorkerMetrics(server)
	_, err = client.Get("http://" + server.Addr + "/metrics")
	require.Error(t, err)
}
