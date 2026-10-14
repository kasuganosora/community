package search

// DiscoveryConnection exposes only server-side connection settings to the
// independent recommendation indexer. Reading it does not initialize indexes,
// wait for the generic search backfill, or alter the generic search schema.
// APIKey must never be included in an API response or log.
type DiscoveryConnection struct {
	Enabled bool
	Host    string
	APIKey  string
	Prefix  string
}

func DiscoveryConnectionConfig() DiscoveryConnection {
	return DiscoveryConnection{Enabled: meiliMode() != "off", Host: getMeilisearchHost(), APIKey: getMeilisearchAPIKey(), Prefix: getMeilisearchIndexPrefix()}
}
