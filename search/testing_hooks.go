package search

// UseMeilisearchForTest points the shared client at host (normally an
// httptest server) and marks it enabled without probing or indexing. It
// returns a function that restores the previous client. Tests only.
func UseMeilisearchForTest(host string) (restore func()) {
	clientOnce.Do(func() {})
	previous := meiliClient
	c := &MeilisearchClient{host: host}
	c.ensureClient()
	c.setEnabled(true)
	meiliClient = c
	return func() { meiliClient = previous }
}
